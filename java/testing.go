// Copyright 2019 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package java

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"android/soong/android"
	"android/soong/cc"
	"android/soong/dexpreopt"

	"github.com/google/blueprint"
)

const defaultJavaDir = "default/java"

// Test fixture preparer that will register most java build components.
//
// Singletons and mutators should only be added here if they are needed for a majority of java
// module types, otherwise they should be added under a separate preparer to allow them to be
// selected only when needed to reduce test execution time.
//
// Module types do not have much of an overhead unless they are used so this should include as many
// module types as possible. The exceptions are those module types that require mutators and/or
// singletons in order to function in which case they should be kept together in a separate
// preparer.
var PrepareForTestWithJavaBuildComponents = android.GroupFixturePreparers(
	// Make sure that mutators and module types, e.g. prebuilt mutators available.
	android.PrepareForTestWithAndroidBuildComponents,
	// Make java build components available to the test.
	android.FixtureRegisterWithContext(registerRequiredBuildComponentsForTest),
	android.FixtureRegisterWithContext(registerJavaPluginBuildComponents),
	// Additional files needed in tests that disallow non-existent source files.
	// This includes files that are needed by all, or at least most, instances of a java module type.
	android.MockFS{
		// Needed for linter used by java_library.
		"build/soong/java/lint_defaults.txt": nil,
		// Needed for apps that do not provide their own.
		"build/make/target/product/security": nil,
		// Required to generate Java used-by API coverage
		"build/soong/scripts/gen_java_usedby_apex.sh": nil,
	}.AddToFixture(),
)

// Test fixture preparer that will define all default java modules except the
// fake_tool_binary for dex2oatd.
var PrepareForTestWithJavaDefaultModulesWithoutFakeDex2oatd = android.GroupFixturePreparers(
	// Make sure that all the module types used in the defaults are registered.
	PrepareForTestWithJavaBuildComponents,
	// Additional files needed when test disallows non-existent source.
	android.MockFS{
		// Needed for framework-res
		defaultJavaDir + "/AndroidManifest.xml": nil,
		// Needed for framework
		defaultJavaDir + "/framework/aidl": nil,
		// Needed for various deps defined in GatherRequiredDepsForTest()
		defaultJavaDir + "/a.java": nil,

		// Needed for R8 rules on apps
		"build/make/core/proguard.flags":             nil,
		"build/make/core/proguard_basic_keeps.flags": nil,
	}.AddToFixture(),
	// The java default module definitions.
	android.FixtureAddTextFile(defaultJavaDir+"/Android.bp", gatherRequiredDepsForTest()),
	// Add dexpreopt compat libs (android.test.base, etc.) and a fake dex2oatd module.
	dexpreopt.PrepareForTestWithDexpreoptCompatLibs,
)

// Test fixture preparer that will define default java modules, e.g. standard prebuilt modules.
var PrepareForTestWithJavaDefaultModules = android.GroupFixturePreparers(
	PrepareForTestWithJavaDefaultModulesWithoutFakeDex2oatd,
	dexpreopt.PrepareForTestWithFakeDex2oatd,
)

// Provides everything needed by dexpreopt.
var PrepareForTestWithDexpreopt = android.GroupFixturePreparers(
	PrepareForTestWithJavaDefaultModules,
	dexpreopt.PrepareForTestByEnablingDexpreopt,
)

var PrepareForTestWithOverlayBuildComponents = android.FixtureRegisterWithContext(registerOverlayBuildComponents)

// Prepare a fixture to use all java module types, mutators and singletons fully.
//
// This should only be used by tests that want to run with as much of the build enabled as possible.
var PrepareForIntegrationTestWithJava = android.GroupFixturePreparers(
	cc.PrepareForIntegrationTestWithCc,
	PrepareForTestWithJavaDefaultModules,
)

// Prepare a fixture with the standard files required by a java_sdk_library module.
var PrepareForTestWithJavaSdkLibraryFiles = android.FixtureMergeMockFs(android.MockFS{
	"api/current.txt":               nil,
	"api/removed.txt":               nil,
	"api/system-current.txt":        nil,
	"api/system-removed.txt":        nil,
	"api/test-current.txt":          nil,
	"api/test-removed.txt":          nil,
	"api/module-lib-current.txt":    nil,
	"api/module-lib-removed.txt":    nil,
	"api/system-server-current.txt": nil,
	"api/system-server-removed.txt": nil,
})

// FixtureWithLastReleaseApis creates a preparer that creates prebuilt versions of the specified
// modules for the `last` API release. By `last` it just means last in the list of supplied versions
// and as this only provides one version it can be any value.
//
// This uses FixtureWithPrebuiltApis under the covers so the limitations of that apply to this.
func FixtureWithLastReleaseApis(moduleNames ...string) android.FixturePreparer {
	return FixtureWithPrebuiltApis(map[string][]string{
		"30": moduleNames,
	})
}

// PrepareForTestWithPrebuiltsOfCurrentApi is a preparer that creates prebuilt versions of the
// standard modules for the current version.
//
// This uses FixtureWithPrebuiltApis under the covers so the limitations of that apply to this.
var PrepareForTestWithPrebuiltsOfCurrentApi = FixtureWithPrebuiltApis(map[string][]string{
	"current": {},
	// Can't have current on its own as it adds a prebuilt_apis module but doesn't add any
	// .txt files which causes the prebuilt_apis module to fail.
	"30": {},
})

// FixtureWithPrebuiltApis creates a preparer that will define prebuilt api modules for the
// specified releases and modules.
//
// The supplied map keys are the releases, e.g. current, 29, 30, etc. The values are a list of
// modules for that release. Due to limitations in the prebuilt_apis module which this preparer
// uses the set of releases must include at least one numbered release, i.e. it cannot just include
// "current".
//
// This defines a file in the mock file system in a predefined location (prebuilts/sdk/Android.bp)
// and so only one instance of this can be used in each fixture.
func FixtureWithPrebuiltApis(release2Modules map[string][]string) android.FixturePreparer {
	return FixtureWithPrebuiltApisAndExtensions(release2Modules, nil)
}

func FixtureWithPrebuiltApisAndExtensions(apiLevel2Modules map[string][]string, extensionLevel2Modules map[string][]string) android.FixturePreparer {
	mockFS := android.MockFS{}
	path := "prebuilts/sdk/Android.bp"

	bp := fmt.Sprintf(`
			prebuilt_apis {
				name: "sdk",
				api_dirs: ["%s"],
				extensions_dir: "extensions",
				imports_sdk_version: "none",
				imports_compile_dex: true,
			}
		`, strings.Join(android.SortedStringKeys(apiLevel2Modules), `", "`))

	for release, modules := range apiLevel2Modules {
		mockFS.Merge(prebuiltApisFilesForModules([]string{release}, modules))
	}
	if extensionLevel2Modules != nil {
		for release, modules := range extensionLevel2Modules {
			mockFS.Merge(prebuiltExtensionApiFiles([]string{release}, modules))
		}
	}
	return android.GroupFixturePreparers(
		android.FixtureAddTextFile(path, bp),
		android.FixtureMergeMockFs(mockFS),
	)
}

func prebuiltApisFilesForModules(apiLevels []string, modules []string) map[string][]byte {
	libs := append([]string{"android"}, modules...)

	fs := make(map[string][]byte)
	for _, level := range apiLevels {
		apiLevel := android.ApiLevelForTest(level)
		for _, sdkKind := range []android.SdkKind{android.SdkPublic, android.SdkSystem, android.SdkModule, android.SdkSystemServer, android.SdkTest} {
			// A core-for-system-modules file must only be created for the sdk kind that supports it.
			if sdkKind == systemModuleKind(sdkKind, apiLevel) {
				fs[fmt.Sprintf("prebuilts/sdk/%s/%s/core-for-system-modules.jar", level, sdkKind)] = nil
			}

			for _, lib := range libs {
				// Create a jar file for every library.
				fs[fmt.Sprintf("prebuilts/sdk/%s/%s/%s.jar", level, sdkKind, lib)] = nil

				// No finalized API files for "current"
				if level != "current" {
					fs[fmt.Sprintf("prebuilts/sdk/%s/%s/api/%s.txt", level, sdkKind, lib)] = nil
					fs[fmt.Sprintf("prebuilts/sdk/%s/%s/api/%s-removed.txt", level, sdkKind, lib)] = nil
				}
			}
		}
		if level == "current" {
			fs["prebuilts/sdk/current/core/android.jar"] = nil
		}
		fs[fmt.Sprintf("prebuilts/sdk/%s/public/framework.aidl", level)] = nil
	}
	return fs
}

func prebuiltExtensionApiFiles(extensionLevels []string, modules []string) map[string][]byte {
	fs := make(map[string][]byte)
	for _, level := range extensionLevels {
		for _, sdkKind := range []android.SdkKind{android.SdkPublic, android.SdkSystem, android.SdkModule, android.SdkSystemServer} {
			for _, lib := range modules {
				fs[fmt.Sprintf("prebuilts/sdk/extensions/%s/%s/api/%s.txt", level, sdkKind, lib)] = nil
				fs[fmt.Sprintf("prebuilts/sdk/extensions/%s/%s/api/%s-removed.txt", level, sdkKind, lib)] = nil
			}
		}
	}
	return fs
}

// FixtureConfigureBootJars configures the boot jars in both the dexpreopt.GlobalConfig and
// Config.productVariables structs. As a side effect that enables dexpreopt.
func FixtureConfigureBootJars(bootJars ...string) android.FixturePreparer {
	artBootJars := []string{}
	for _, j := range bootJars {
		artApex := false
		for _, artApexName := range artApexNames {
			if strings.HasPrefix(j, artApexName+":") {
				artApex = true
				break
			}
		}
		if artApex {
			artBootJars = append(artBootJars, j)
		}
	}
	return android.GroupFixturePreparers(
		android.FixtureModifyProductVariables(func(variables android.FixtureProductVariables) {
			variables.BootJars = android.CreateTestConfiguredJarList(bootJars)
		}),
		dexpreopt.FixtureSetBootJars(bootJars...),
		dexpreopt.FixtureSetArtBootJars(artBootJars...),

		// Add a fake dex2oatd module.
		dexpreopt.PrepareForTestWithFakeDex2oatd,
	)
}

// FixtureConfigureApexBootJars configures the apex boot jars in both the
// dexpreopt.GlobalConfig and Config.productVariables structs. As a side effect that enables
// dexpreopt.
func FixtureConfigureApexBootJars(bootJars ...string) android.FixturePreparer {
	return android.GroupFixturePreparers(
		android.FixtureModifyProductVariables(func(variables android.FixtureProductVariables) {
			variables.ApexBootJars = android.CreateTestConfiguredJarList(bootJars)
		}),
		dexpreopt.FixtureSetApexBootJars(bootJars...),

		// Add a fake dex2oatd module.
		dexpreopt.PrepareForTestWithFakeDex2oatd,
	)
}

// FixtureUseLegacyCorePlatformApi prepares the fixture by setting the exception list of those
// modules that are allowed to use the legacy core platform API to be the ones supplied.
func FixtureUseLegacyCorePlatformApi(moduleNames ...string) android.FixturePreparer {
	lookup := make(map[string]struct{})
	for _, moduleName := range moduleNames {
		lookup[moduleName] = struct{}{}
	}
	return android.FixtureModifyConfig(func(config android.Config) {
		// Try and set the legacyCorePlatformApiLookup in the config, the returned value will be the
		// actual value that is set.
		cached := config.Once(legacyCorePlatformApiLookupKey, func() interface{} {
			return lookup
		})
		// Make sure that the cached value is the one we need.
		if !reflect.DeepEqual(cached, lookup) {
			panic(fmt.Errorf("attempting to set legacyCorePlatformApiLookupKey to %q but it has already been set to %q", lookup, cached))
		}
	})
}

// registerRequiredBuildComponentsForTest registers the build components used by
// PrepareForTestWithJavaDefaultModules.
//
// As functionality is moved out of here into separate FixturePreparer instances they should also
// be moved into GatherRequiredDepsForTest for use by tests that have not yet switched to use test
// fixtures.
func registerRequiredBuildComponentsForTest(ctx android.RegistrationContext) {
	RegisterAARBuildComponents(ctx)
	RegisterAppBuildComponents(ctx)
	RegisterAppImportBuildComponents(ctx)
	RegisterAppSetBuildComponents(ctx)
	registerBootclasspathBuildComponents(ctx)
	registerBootclasspathFragmentBuildComponents(ctx)
	RegisterDexpreoptBootJarsComponents(ctx)
	RegisterDocsBuildComponents(ctx)
	RegisterGenRuleBuildComponents(ctx)
	registerJavaBuildComponents(ctx)
	registerPlatformBootclasspathBuildComponents(ctx)
	RegisterPrebuiltApisBuildComponents(ctx)
	RegisterRuntimeResourceOverlayBuildComponents(ctx)
	RegisterSdkLibraryBuildComponents(ctx)
	RegisterStubsBuildComponents(ctx)
	RegisterSystemModulesBuildComponents(ctx)
	registerSystemserverClasspathBuildComponents(ctx)
	registerLintBuildComponents(ctx)
}

// gatherRequiredDepsForTest gathers the module definitions used by
// PrepareForTestWithJavaDefaultModules.
//
// As functionality is moved out of here into separate FixturePreparer instances they should also
// be moved into GatherRequiredDepsForTest for use by tests that have not yet switched to use test
// fixtures.
func gatherRequiredDepsForTest() string {
	var bp string

	extraModules := []string{
		"core-lambda-stubs",
		"ext",
		"android_stubs_current",
		"android_system_stubs_current",
		"android_test_stubs_current",
		"android_module_lib_stubs_current",
		"android_system_server_stubs_current",
		"core.current.stubs",
		"legacy.core.platform.api.stubs",
		"stable.core.platform.api.stubs",
		"kotlin-stdlib",
		"kotlin-stdlib-jdk7",
		"kotlin-stdlib-jdk8",
		"kotlin-annotations",
		"stub-annotations",
	}

	for _, extra := range extraModules {
		bp += fmt.Sprintf(`
			java_library {
				name: "%s",
				srcs: ["a.java"],
				sdk_version: "none",
				system_modules: "stable-core-platform-api-stubs-system-modules",
				compile_dex: true,
			}
		`, extra)
	}

	bp += `
		java_library {
			name: "framework",
			srcs: ["a.java"],
			sdk_version: "none",
			system_modules: "stable-core-platform-api-stubs-system-modules",
			aidl: {
				export_include_dirs: ["framework/aidl"],
			},
		}

		android_app {
			name: "framework-res",
			sdk_version: "core_platform",
		}

		android_app {
			name: "com.evervolv.platform-res",
			sdk_version: "core_platform",
		}`

	systemModules := []string{
		"core-public-stubs-system-modules",
		"core-module-lib-stubs-system-modules",
		"legacy-core-platform-api-stubs-system-modules",
		"stable-core-platform-api-stubs-system-modules",
	}

	for _, extra := range systemModules {
		bp += fmt.Sprintf(`
			java_system_modules {
				name: "%[1]s",
				libs: ["%[1]s-lib"],
			}
			java_library {
				name: "%[1]s-lib",
				sdk_version: "none",
				system_modules: "none",
			}
		`, extra)
	}

	// Make sure that the dex_bootjars singleton module is instantiated for the tests.
	bp += `
		dex_bootjars {
			name: "dex_bootjars",
		}
`

	return bp
}

func CheckModuleDependencies(t *testing.T, ctx *android.TestContext, name, variant string, expected []string) {
	t.Helper()
	module := ctx.ModuleForTests(name, variant).Module()
	deps := []string{}
	ctx.VisitDirectDeps(module, func(m blueprint.Module) {
		deps = append(deps, m.Name())
	})
	sort.Strings(deps)

	if actual := deps; !reflect.DeepEqual(expected, actual) {
		t.Errorf("expected %#q, found %#q", expected, actual)
	}
}

// CheckPlatformBootclasspathModules returns the apex:module pair for the modules depended upon by
// the platform-bootclasspath module.
func CheckPlatformBootclasspathModules(t *testing.T, result *android.TestResult, name string, expected []string) {
	t.Helper()
	platformBootclasspath := result.Module(name, "android_common").(*platformBootclasspathModule)
	pairs := ApexNamePairsFromModules(result.TestContext, platformBootclasspath.configuredModules)
	android.AssertDeepEquals(t, fmt.Sprintf("%s modules", "platform-bootclasspath"), expected, pairs)
}

func CheckClasspathFragmentProtoContentInfoProvider(t *testing.T, result *android.TestResult, generated bool, contents, outputFilename, installDir string) {
	t.Helper()
	p := result.Module("platform-bootclasspath", "android_common").(*platformBootclasspathModule)
	info := result.ModuleProvider(p, ClasspathFragmentProtoContentInfoProvider).(ClasspathFragmentProtoContentInfo)

	android.AssertBoolEquals(t, "classpath proto generated", generated, info.ClasspathFragmentProtoGenerated)
	android.AssertStringEquals(t, "classpath proto contents", contents, info.ClasspathFragmentProtoContents.String())
	android.AssertStringEquals(t, "output filepath", outputFilename, info.ClasspathFragmentProtoOutput.Base())
	android.AssertPathRelativeToTopEquals(t, "install filepath", installDir, info.ClasspathFragmentProtoInstallDir)
}

// ApexNamePairsFromModules returns the apex:module pair for the supplied modules.
func ApexNamePairsFromModules(ctx *android.TestContext, modules []android.Module) []string {
	pairs := []string{}
	for _, module := range modules {
		pairs = append(pairs, apexNamePairFromModule(ctx, module))
	}
	return pairs
}

func apexNamePairFromModule(ctx *android.TestContext, module android.Module) string {
	name := module.Name()
	var apex string
	apexInfo := ctx.ModuleProvider(module, android.ApexInfoProvider).(android.ApexInfo)
	if apexInfo.IsForPlatform() {
		apex = "platform"
	} else {
		apex = apexInfo.InApexVariants[0]
	}

	return fmt.Sprintf("%s:%s", apex, name)
}

// CheckPlatformBootclasspathFragments returns the apex:module pair for the fragments depended upon
// by the platform-bootclasspath module.
func CheckPlatformBootclasspathFragments(t *testing.T, result *android.TestResult, name string, expected []string) {
	t.Helper()
	platformBootclasspath := result.Module(name, "android_common").(*platformBootclasspathModule)
	pairs := ApexNamePairsFromModules(result.TestContext, platformBootclasspath.fragments)
	android.AssertDeepEquals(t, fmt.Sprintf("%s fragments", "platform-bootclasspath"), expected, pairs)
}

func CheckHiddenAPIRuleInputs(t *testing.T, message string, expected string, hiddenAPIRule android.TestingBuildParams) {
	t.Helper()
	inputs := android.Paths{}
	if hiddenAPIRule.Input != nil {
		inputs = append(inputs, hiddenAPIRule.Input)
	}
	inputs = append(inputs, hiddenAPIRule.Inputs...)
	inputs = append(inputs, hiddenAPIRule.Implicits...)
	inputs = android.SortedUniquePaths(inputs)
	actual := strings.TrimSpace(strings.Join(inputs.RelativeToTop().Strings(), "\n"))
	re := regexp.MustCompile(`\n\s+`)
	expected = strings.TrimSpace(re.ReplaceAllString(expected, "\n"))
	if actual != expected {
		t.Errorf("Expected hiddenapi rule inputs - %s:\n%s\nactual inputs:\n%s", message, expected, actual)
	}
}

// Check that the merged file create by platform_compat_config_singleton has the correct inputs.
func CheckMergedCompatConfigInputs(t *testing.T, result *android.TestResult, message string, expectedPaths ...string) {
	sourceGlobalCompatConfig := result.SingletonForTests("platform_compat_config_singleton")
	allOutputs := sourceGlobalCompatConfig.AllOutputs()
	android.AssertIntEquals(t, message+": output len", 1, len(allOutputs))
	output := sourceGlobalCompatConfig.Output(allOutputs[0])
	android.AssertPathsRelativeToTopEquals(t, message+": inputs", expectedPaths, output.Implicits)
}

// Register the fake APEX mutator to `android.InitRegistrationContext` as if the real mutator exists
// at runtime. This must be called in `init()` of a test if the test is going to use the fake APEX
// mutator. Otherwise, we will be missing the runtime mutator because "soong-apex" is not a
// dependency, which will cause an inconsistency between testing and runtime mutators.
func RegisterFakeRuntimeApexMutator() {
	registerFakeApexMutator(android.InitRegistrationContext)
}

var PrepareForTestWithFakeApexMutator = android.GroupFixturePreparers(
	android.FixtureRegisterWithContext(registerFakeApexMutator),
)

func registerFakeApexMutator(ctx android.RegistrationContext) {
	ctx.PostDepsMutators(func(ctx android.RegisterMutatorsContext) {
		ctx.BottomUp("apex", fakeApexMutator).Parallel()
	})
}

type apexModuleBase interface {
	ApexAvailable() []string
}

var _ apexModuleBase = (*Library)(nil)
var _ apexModuleBase = (*SdkLibrary)(nil)

// A fake APEX mutator that creates a platform variant and an APEX variant for modules with
// `apex_available`. It helps us avoid a dependency on the real mutator defined in "soong-apex",
// which will cause a cyclic dependency, and it provides an easy way to create an APEX variant for
// testing without dealing with all the complexities in the real mutator.
func fakeApexMutator(mctx android.BottomUpMutatorContext) {
	switch mctx.Module().(type) {
	case *Library, *SdkLibrary:
		if len(mctx.Module().(apexModuleBase).ApexAvailable()) > 0 {
			modules := mctx.CreateVariations("", "apex1000")
			apexInfo := android.ApexInfo{
				ApexVariationName: "apex1000",
			}
			mctx.SetVariationProvider(modules[1], android.ApexInfoProvider, apexInfo)
		}
	}
}

// Applies the given modifier on the boot image config with the given name.
func FixtureModifyBootImageConfig(name string, configModifier func(*bootImageConfig)) android.FixturePreparer {
	return android.FixtureModifyConfig(func(androidConfig android.Config) {
		pathCtx := android.PathContextForTesting(androidConfig)
		config := genBootImageConfigRaw(pathCtx)
		configModifier(config[name])
	})
}

// Sets the value of `installDirOnDevice` of the boot image config with the given name.
func FixtureSetBootImageInstallDirOnDevice(name string, installDir string) android.FixturePreparer {
	return FixtureModifyBootImageConfig(name, func(config *bootImageConfig) {
		config.installDirOnDevice = installDir
	})
}
