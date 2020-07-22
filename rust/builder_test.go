// Copyright 2020 The Android Open Source Project
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

package rust

import "testing"

func TestSourceProviderCollision(t *testing.T) {
	testRustError(t, "multiple source providers generate the same filename output: bindings.rs", `
		rust_binary {
			name: "source_collider",
			srcs: [
				"foo.rs",
				":libbindings1",
				":libbindings2",
			],
		}
		rust_bindgen {
			name: "libbindings1",
			stem: "bindings",
			wrapper_src: "src/any.h",
		}
		rust_bindgen {
			name: "libbindings2",
			stem: "bindings",
			wrapper_src: "src/any.h",
		}
	`)
}
