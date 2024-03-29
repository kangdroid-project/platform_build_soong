// Copyright 2017 Google Inc. All rights reserved.
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
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/cc"
)

var (
	resourceFiles = []string{
		"res/layout/layout.xml",
		"res/values/strings.xml",
		"res/values-en-rUS/strings.xml",
	}

	compiledResourceFiles = []string{
		"aapt2/res/layout_layout.xml.flat",
		"aapt2/res/values_strings.arsc.flat",
		"aapt2/res/values-en-rUS_strings.arsc.flat",
	}
)

func testAppContext(config android.Config, bp string, fs map[string][]byte) *android.TestContext {
	appFS := map[string][]byte{}
	for k, v := range fs {
		appFS[k] = v
	}

	for _, file := range resourceFiles {
		appFS[file] = nil
	}

	return testContext(config, bp, appFS)
}

func testApp(t *testing.T, bp string) *android.TestContext {
	config := testConfig(nil)

	ctx := testAppContext(config, bp, nil)

	run(t, ctx, config)

	return ctx
}

func TestApp(t *testing.T) {
	for _, moduleType := range []string{"android_app", "android_library"} {
		t.Run(moduleType, func(t *testing.T) {
			ctx := testApp(t, moduleType+` {
					name: "foo",
					srcs: ["a.java"],
				}
			`)

			foo := ctx.ModuleForTests("foo", "android_common")

			var expectedLinkImplicits []string

			manifestFixer := foo.Output("manifest_fixer/AndroidManifest.xml")
			expectedLinkImplicits = append(expectedLinkImplicits, manifestFixer.Output.String())

			frameworkRes := ctx.ModuleForTests("framework-res", "android_common")
			expectedLinkImplicits = append(expectedLinkImplicits,
				frameworkRes.Output("package-res.apk").Output.String())

			// Test the mapping from input files to compiled output file names
			compile := foo.Output(compiledResourceFiles[0])
			if !reflect.DeepEqual(resourceFiles, compile.Inputs.Strings()) {
				t.Errorf("expected aapt2 compile inputs expected:\n  %#v\n got:\n  %#v",
					resourceFiles, compile.Inputs.Strings())
			}

			compiledResourceOutputs := compile.Outputs.Strings()
			sort.Strings(compiledResourceOutputs)

			expectedLinkImplicits = append(expectedLinkImplicits, compiledResourceOutputs...)

			list := foo.Output("aapt2/res.list")
			expectedLinkImplicits = append(expectedLinkImplicits, list.Output.String())

			// Check that the link rule uses
			res := ctx.ModuleForTests("foo", "android_common").Output("package-res.apk")
			if !reflect.DeepEqual(expectedLinkImplicits, res.Implicits.Strings()) {
				t.Errorf("expected aapt2 link implicits expected:\n  %#v\n got:\n  %#v",
					expectedLinkImplicits, res.Implicits.Strings())
			}
		})
	}
}

func TestAppSplits(t *testing.T) {
	ctx := testApp(t, `
				android_app {
					name: "foo",
					srcs: ["a.java"],
					package_splits: ["v4", "v7,hdpi"],
				}`)

	foo := ctx.ModuleForTests("foo", "android_common")

	expectedOutputs := []string{
		filepath.Join(buildDir, ".intermediates/foo/android_common/foo.apk"),
		filepath.Join(buildDir, ".intermediates/foo/android_common/foo_v4.apk"),
		filepath.Join(buildDir, ".intermediates/foo/android_common/foo_v7_hdpi.apk"),
	}
	for _, expectedOutput := range expectedOutputs {
		foo.Output(expectedOutput)
	}

	outputFiles, err := foo.Module().(*AndroidApp).OutputFiles("")
	if err != nil {
		t.Fatal(err)
	}
	if g, w := outputFiles.Strings(), expectedOutputs; !reflect.DeepEqual(g, w) {
		t.Errorf(`want OutputFiles("") = %q, got %q`, w, g)
	}
}

func TestResourceDirs(t *testing.T) {
	testCases := []struct {
		name      string
		prop      string
		resources []string
	}{
		{
			name:      "no resource_dirs",
			prop:      "",
			resources: []string{"res/res/values/strings.xml"},
		},
		{
			name:      "resource_dirs",
			prop:      `resource_dirs: ["res"]`,
			resources: []string{"res/res/values/strings.xml"},
		},
		{
			name:      "empty resource_dirs",
			prop:      `resource_dirs: []`,
			resources: nil,
		},
	}

	fs := map[string][]byte{
		"res/res/values/strings.xml": nil,
	}

	bp := `
			android_app {
				name: "foo",
				%s
			}
		`

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := testConfig(nil)
			ctx := testContext(config, fmt.Sprintf(bp, testCase.prop), fs)
			run(t, ctx, config)

			module := ctx.ModuleForTests("foo", "android_common")
			resourceList := module.MaybeOutput("aapt2/res.list")

			var resources []string
			if resourceList.Rule != nil {
				for _, compiledResource := range resourceList.Inputs.Strings() {
					resources = append(resources, module.Output(compiledResource).Inputs.Strings()...)
				}
			}

			if !reflect.DeepEqual(resources, testCase.resources) {
				t.Errorf("expected resource files %q, got %q",
					testCase.resources, resources)
			}
		})
	}
}

func TestAndroidResources(t *testing.T) {
	testCases := []struct {
		name                       string
		enforceRROTargets          []string
		enforceRROExcludedOverlays []string
		resourceFiles              map[string][]string
		overlayFiles               map[string][]string
		rroDirs                    map[string][]string
	}{
		{
			name:                       "no RRO",
			enforceRROTargets:          nil,
			enforceRROExcludedOverlays: nil,
			resourceFiles: map[string][]string{
				"foo":  nil,
				"bar":  {"bar/res/res/values/strings.xml"},
				"lib":  nil,
				"lib2": {"lib2/res/res/values/strings.xml"},
			},
			overlayFiles: map[string][]string{
				"foo": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					buildDir + "/.intermediates/lib/android_common/package-res.apk",
					buildDir + "/.intermediates/lib3/android_common/package-res.apk",
					"foo/res/res/values/strings.xml",
					"device/vendor/blah/static_overlay/foo/res/values/strings.xml",
					"device/vendor/blah/overlay/foo/res/values/strings.xml",
					"product/vendor/blah/overlay/foo/res/values/strings.xml",
				},
				"bar": {
					"device/vendor/blah/static_overlay/bar/res/values/strings.xml",
					"device/vendor/blah/overlay/bar/res/values/strings.xml",
				},
				"lib": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					"lib/res/res/values/strings.xml",
					"device/vendor/blah/overlay/lib/res/values/strings.xml",
				},
			},
			rroDirs: map[string][]string{
				"foo": nil,
				"bar": nil,
			},
		},
		{
			name:                       "enforce RRO on foo",
			enforceRROTargets:          []string{"foo"},
			enforceRROExcludedOverlays: []string{"device/vendor/blah/static_overlay"},
			resourceFiles: map[string][]string{
				"foo":  nil,
				"bar":  {"bar/res/res/values/strings.xml"},
				"lib":  nil,
				"lib2": {"lib2/res/res/values/strings.xml"},
			},
			overlayFiles: map[string][]string{
				"foo": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					buildDir + "/.intermediates/lib/android_common/package-res.apk",
					buildDir + "/.intermediates/lib3/android_common/package-res.apk",
					"foo/res/res/values/strings.xml",
					"device/vendor/blah/static_overlay/foo/res/values/strings.xml",
				},
				"bar": {
					"device/vendor/blah/static_overlay/bar/res/values/strings.xml",
					"device/vendor/blah/overlay/bar/res/values/strings.xml",
				},
				"lib": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					"lib/res/res/values/strings.xml",
					"device/vendor/blah/overlay/lib/res/values/strings.xml",
				},
			},

			rroDirs: map[string][]string{
				"foo": {
					"device:device/vendor/blah/overlay/foo/res",
					// Enforce RRO on "foo" could imply RRO on static dependencies, but for now it doesn't.
					// "device/vendor/blah/overlay/lib/res",
					"product:product/vendor/blah/overlay/foo/res",
				},
				"bar": nil,
				"lib": nil,
			},
		},
		{
			name:              "enforce RRO on all",
			enforceRROTargets: []string{"*"},
			enforceRROExcludedOverlays: []string{
				// Excluding specific apps/res directories also allowed.
				"device/vendor/blah/static_overlay/foo",
				"device/vendor/blah/static_overlay/bar/res",
			},
			resourceFiles: map[string][]string{
				"foo":  nil,
				"bar":  {"bar/res/res/values/strings.xml"},
				"lib":  nil,
				"lib2": {"lib2/res/res/values/strings.xml"},
			},
			overlayFiles: map[string][]string{
				"foo": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					buildDir + "/.intermediates/lib/android_common/package-res.apk",
					buildDir + "/.intermediates/lib3/android_common/package-res.apk",
					"foo/res/res/values/strings.xml",
					"device/vendor/blah/static_overlay/foo/res/values/strings.xml",
				},
				"bar": {"device/vendor/blah/static_overlay/bar/res/values/strings.xml"},
				"lib": {
					buildDir + "/.intermediates/lib2/android_common/package-res.apk",
					"lib/res/res/values/strings.xml",
				},
			},
			rroDirs: map[string][]string{
				"foo": {
					"device:device/vendor/blah/overlay/foo/res",
					"product:product/vendor/blah/overlay/foo/res",
					// Lib dep comes after the direct deps
					"device:device/vendor/blah/overlay/lib/res",
				},
				"bar": {"device:device/vendor/blah/overlay/bar/res"},
				"lib": {"device:device/vendor/blah/overlay/lib/res"},
			},
		},
	}

	deviceResourceOverlays := []string{
		"device/vendor/blah/overlay",
		"device/vendor/blah/overlay2",
		"device/vendor/blah/static_overlay",
	}

	productResourceOverlays := []string{
		"product/vendor/blah/overlay",
	}

	fs := map[string][]byte{
		"foo/res/res/values/strings.xml":                               nil,
		"bar/res/res/values/strings.xml":                               nil,
		"lib/res/res/values/strings.xml":                               nil,
		"lib2/res/res/values/strings.xml":                              nil,
		"device/vendor/blah/overlay/foo/res/values/strings.xml":        nil,
		"device/vendor/blah/overlay/bar/res/values/strings.xml":        nil,
		"device/vendor/blah/overlay/lib/res/values/strings.xml":        nil,
		"device/vendor/blah/static_overlay/foo/res/values/strings.xml": nil,
		"device/vendor/blah/static_overlay/bar/res/values/strings.xml": nil,
		"device/vendor/blah/overlay2/res/values/strings.xml":           nil,
		"product/vendor/blah/overlay/foo/res/values/strings.xml":       nil,
	}

	bp := `
			android_app {
				name: "foo",
				resource_dirs: ["foo/res"],
				static_libs: ["lib", "lib3"],
			}

			android_app {
				name: "bar",
				resource_dirs: ["bar/res"],
			}

			android_library {
				name: "lib",
				resource_dirs: ["lib/res"],
				static_libs: ["lib2"],
			}

			android_library {
				name: "lib2",
				resource_dirs: ["lib2/res"],
			}

			// This library has the same resources as lib (should not lead to dupe RROs)
			android_library {
				name: "lib3",
				resource_dirs: ["lib/res"]
			}
		`

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := testConfig(nil)
			config.TestProductVariables.DeviceResourceOverlays = deviceResourceOverlays
			config.TestProductVariables.ProductResourceOverlays = productResourceOverlays
			if testCase.enforceRROTargets != nil {
				config.TestProductVariables.EnforceRROTargets = testCase.enforceRROTargets
			}
			if testCase.enforceRROExcludedOverlays != nil {
				config.TestProductVariables.EnforceRROExcludedOverlays = testCase.enforceRROExcludedOverlays
			}

			ctx := testAppContext(config, bp, fs)
			run(t, ctx, config)

			resourceListToFiles := func(module android.TestingModule, list []string) (files []string) {
				for _, o := range list {
					res := module.MaybeOutput(o)
					if res.Rule != nil {
						// If the overlay is compiled as part of this module (i.e. a .arsc.flat file),
						// verify the inputs to the .arsc.flat rule.
						files = append(files, res.Inputs.Strings()...)
					} else {
						// Otherwise, verify the full path to the output of the other module
						files = append(files, o)
					}
				}
				return files
			}

			getResources := func(moduleName string) (resourceFiles, overlayFiles, rroDirs []string) {
				module := ctx.ModuleForTests(moduleName, "android_common")
				resourceList := module.MaybeOutput("aapt2/res.list")
				if resourceList.Rule != nil {
					resourceFiles = resourceListToFiles(module, resourceList.Inputs.Strings())
				}
				overlayList := module.MaybeOutput("aapt2/overlay.list")
				if overlayList.Rule != nil {
					overlayFiles = resourceListToFiles(module, overlayList.Inputs.Strings())
				}

				for _, d := range module.Module().(AndroidLibraryDependency).ExportedRRODirs() {
					var prefix string
					if d.overlayType == device {
						prefix = "device:"
					} else if d.overlayType == product {
						prefix = "product:"
					} else {
						t.Fatalf("Unexpected overlayType %d", d.overlayType)
					}
					rroDirs = append(rroDirs, prefix+d.path.String())
				}

				return resourceFiles, overlayFiles, rroDirs
			}

			modules := []string{"foo", "bar", "lib", "lib2"}
			for _, module := range modules {
				resourceFiles, overlayFiles, rroDirs := getResources(module)

				if !reflect.DeepEqual(resourceFiles, testCase.resourceFiles[module]) {
					t.Errorf("expected %s resource files:\n  %#v\n got:\n  %#v",
						module, testCase.resourceFiles[module], resourceFiles)
				}
				if !reflect.DeepEqual(overlayFiles, testCase.overlayFiles[module]) {
					t.Errorf("expected %s overlay files:\n  %#v\n got:\n  %#v",
						module, testCase.overlayFiles[module], overlayFiles)
				}
				if !reflect.DeepEqual(rroDirs, testCase.rroDirs[module]) {
					t.Errorf("expected %s rroDirs:  %#v\n got:\n  %#v",
						module, testCase.rroDirs[module], rroDirs)
				}
			}
		})
	}
}

func TestAppSdkVersion(t *testing.T) {
	testCases := []struct {
		name                  string
		sdkVersion            string
		platformSdkInt        int
		platformSdkCodename   string
		platformSdkFinal      bool
		expectedMinSdkVersion string
	}{
		{
			name:                  "current final SDK",
			sdkVersion:            "current",
			platformSdkInt:        27,
			platformSdkCodename:   "REL",
			platformSdkFinal:      true,
			expectedMinSdkVersion: "27",
		},
		{
			name:                  "current non-final SDK",
			sdkVersion:            "current",
			platformSdkInt:        27,
			platformSdkCodename:   "OMR1",
			platformSdkFinal:      false,
			expectedMinSdkVersion: "OMR1",
		},
		{
			name:                  "default final SDK",
			sdkVersion:            "",
			platformSdkInt:        27,
			platformSdkCodename:   "REL",
			platformSdkFinal:      true,
			expectedMinSdkVersion: "27",
		},
		{
			name:                  "default non-final SDK",
			sdkVersion:            "",
			platformSdkInt:        27,
			platformSdkCodename:   "OMR1",
			platformSdkFinal:      false,
			expectedMinSdkVersion: "OMR1",
		},
		{
			name:                  "14",
			sdkVersion:            "14",
			expectedMinSdkVersion: "14",
		},
	}

	for _, moduleType := range []string{"android_app", "android_library"} {
		for _, test := range testCases {
			t.Run(moduleType+" "+test.name, func(t *testing.T) {
				bp := fmt.Sprintf(`%s {
					name: "foo",
					srcs: ["a.java"],
					sdk_version: "%s",
				}`, moduleType, test.sdkVersion)

				config := testConfig(nil)
				config.TestProductVariables.Platform_sdk_version = &test.platformSdkInt
				config.TestProductVariables.Platform_sdk_codename = &test.platformSdkCodename
				config.TestProductVariables.Platform_sdk_final = &test.platformSdkFinal

				ctx := testAppContext(config, bp, nil)

				run(t, ctx, config)

				foo := ctx.ModuleForTests("foo", "android_common")
				link := foo.Output("package-res.apk")
				linkFlags := strings.Split(link.Args["flags"], " ")
				min := android.IndexList("--min-sdk-version", linkFlags)
				target := android.IndexList("--target-sdk-version", linkFlags)

				if min == -1 || target == -1 || min == len(linkFlags)-1 || target == len(linkFlags)-1 {
					t.Fatalf("missing --min-sdk-version or --target-sdk-version in link flags: %q", linkFlags)
				}

				gotMinSdkVersion := linkFlags[min+1]
				gotTargetSdkVersion := linkFlags[target+1]

				if gotMinSdkVersion != test.expectedMinSdkVersion {
					t.Errorf("incorrect --min-sdk-version, expected %q got %q",
						test.expectedMinSdkVersion, gotMinSdkVersion)
				}

				if gotTargetSdkVersion != test.expectedMinSdkVersion {
					t.Errorf("incorrect --target-sdk-version, expected %q got %q",
						test.expectedMinSdkVersion, gotTargetSdkVersion)
				}
			})
		}
	}
}

func TestJNIABI_no_framework_libs_true(t *testing.T) {
	ctx := testJava(t, cc.GatherRequiredDepsForTest(android.Android)+`
		cc_library {
			name: "libjni",
			system_shared_libs: [],
			stl: "none",
		}

		android_test {
			name: "test",
			no_framework_libs: true,
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_first",
			no_framework_libs: true,
			compile_multilib: "first",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_both",
			no_framework_libs: true,
			compile_multilib: "both",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_32",
			no_framework_libs: true,
			compile_multilib: "32",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_64",
			no_framework_libs: true,
			compile_multilib: "64",
			jni_libs: ["libjni"],
		}
		`)

	testCases := []struct {
		name string
		abis []string
	}{
		{"test", []string{"arm64-v8a"}},
		{"test_first", []string{"arm64-v8a"}},
		{"test_both", []string{"arm64-v8a", "armeabi-v7a"}},
		{"test_32", []string{"armeabi-v7a"}},
		{"test_64", []string{"arm64-v8a"}},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			app := ctx.ModuleForTests(test.name, "android_common")
			jniLibZip := app.Output("jnilibs.zip")
			var abis []string
			args := strings.Fields(jniLibZip.Args["jarArgs"])
			for i := 0; i < len(args); i++ {
				if args[i] == "-P" {
					abis = append(abis, filepath.Base(args[i+1]))
					i++
				}
			}
			if !reflect.DeepEqual(abis, test.abis) {
				t.Errorf("want abis %v, got %v", test.abis, abis)
			}
		})
	}
}

func TestJNIABI(t *testing.T) {
	ctx := testJava(t, cc.GatherRequiredDepsForTest(android.Android)+`
		cc_library {
			name: "libjni",
			system_shared_libs: [],
			stl: "none",
		}

		android_test {
			name: "test",
			sdk_version: "core_platform",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_first",
			sdk_version: "core_platform",
			compile_multilib: "first",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_both",
			sdk_version: "core_platform",
			compile_multilib: "both",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_32",
			sdk_version: "core_platform",
			compile_multilib: "32",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_64",
			sdk_version: "core_platform",
			compile_multilib: "64",
			jni_libs: ["libjni"],
		}
		`)

	testCases := []struct {
		name string
		abis []string
	}{
		{"test", []string{"arm64-v8a"}},
		{"test_first", []string{"arm64-v8a"}},
		{"test_both", []string{"arm64-v8a", "armeabi-v7a"}},
		{"test_32", []string{"armeabi-v7a"}},
		{"test_64", []string{"arm64-v8a"}},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			app := ctx.ModuleForTests(test.name, "android_common")
			jniLibZip := app.Output("jnilibs.zip")
			var abis []string
			args := strings.Fields(jniLibZip.Args["jarArgs"])
			for i := 0; i < len(args); i++ {
				if args[i] == "-P" {
					abis = append(abis, filepath.Base(args[i+1]))
					i++
				}
			}
			if !reflect.DeepEqual(abis, test.abis) {
				t.Errorf("want abis %v, got %v", test.abis, abis)
			}
		})
	}
}

func TestJNIPackaging_no_framework_libs_true(t *testing.T) {
	ctx := testJava(t, cc.GatherRequiredDepsForTest(android.Android)+`
		cc_library {
			name: "libjni",
			system_shared_libs: [],
			stl: "none",
		}

		android_app {
			name: "app",
			jni_libs: ["libjni"],
		}

		android_app {
			name: "app_noembed",
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}

		android_app {
			name: "app_embed",
			jni_libs: ["libjni"],
			use_embedded_native_libs: true,
		}

		android_test {
			name: "test",
			no_framework_libs: true,
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_noembed",
			no_framework_libs: true,
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}

		android_test_helper_app {
			name: "test_helper",
			no_framework_libs: true,
			jni_libs: ["libjni"],
		}

		android_test_helper_app {
			name: "test_helper_noembed",
			no_framework_libs: true,
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}
		`)

	testCases := []struct {
		name       string
		packaged   bool
		compressed bool
	}{
		{"app", false, false},
		{"app_noembed", false, false},
		{"app_embed", true, false},
		{"test", true, false},
		{"test_noembed", true, true},
		{"test_helper", true, false},
		{"test_helper_noembed", true, true},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			app := ctx.ModuleForTests(test.name, "android_common")
			jniLibZip := app.MaybeOutput("jnilibs.zip")
			if g, w := (jniLibZip.Rule != nil), test.packaged; g != w {
				t.Errorf("expected jni packaged %v, got %v", w, g)
			}

			if jniLibZip.Rule != nil {
				if g, w := !strings.Contains(jniLibZip.Args["jarArgs"], "-L 0"), test.compressed; g != w {
					t.Errorf("expected jni compressed %v, got %v", w, g)
				}
			}
		})
	}
}

func TestJNIPackaging(t *testing.T) {
	ctx := testJava(t, cc.GatherRequiredDepsForTest(android.Android)+`
		cc_library {
			name: "libjni",
			system_shared_libs: [],
			stl: "none",
		}

		android_app {
			name: "app",
			jni_libs: ["libjni"],
		}

		android_app {
			name: "app_noembed",
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}

		android_app {
			name: "app_embed",
			jni_libs: ["libjni"],
			use_embedded_native_libs: true,
		}

		android_test {
			name: "test",
			sdk_version: "core_platform",
			jni_libs: ["libjni"],
		}

		android_test {
			name: "test_noembed",
			sdk_version: "core_platform",
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}

		android_test_helper_app {
			name: "test_helper",
			sdk_version: "core_platform",
			jni_libs: ["libjni"],
		}

		android_test_helper_app {
			name: "test_helper_noembed",
			sdk_version: "core_platform",
			jni_libs: ["libjni"],
			use_embedded_native_libs: false,
		}
		`)

	testCases := []struct {
		name       string
		packaged   bool
		compressed bool
	}{
		{"app", false, false},
		{"app_noembed", false, false},
		{"app_embed", true, false},
		{"test", true, false},
		{"test_noembed", true, true},
		{"test_helper", true, false},
		{"test_helper_noembed", true, true},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			app := ctx.ModuleForTests(test.name, "android_common")
			jniLibZip := app.MaybeOutput("jnilibs.zip")
			if g, w := (jniLibZip.Rule != nil), test.packaged; g != w {
				t.Errorf("expected jni packaged %v, got %v", w, g)
			}

			if jniLibZip.Rule != nil {
				if g, w := !strings.Contains(jniLibZip.Args["jarArgs"], "-L 0"), test.compressed; g != w {
					t.Errorf("expected jni compressed %v, got %v", w, g)
				}
			}
		})
	}
}

func TestCertificates(t *testing.T) {
	testCases := []struct {
		name                string
		bp                  string
		certificateOverride string
		expected            string
	}{
		{
			name: "default",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
				}
			`,
			certificateOverride: "",
			expected:            "build/make/target/product/security/testkey.x509.pem build/make/target/product/security/testkey.pk8",
		},
		{
			name: "module certificate property",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
					certificate: ":new_certificate"
				}

				android_app_certificate {
					name: "new_certificate",
			    certificate: "cert/new_cert",
				}
			`,
			certificateOverride: "",
			expected:            "cert/new_cert.x509.pem cert/new_cert.pk8",
		},
		{
			name: "path certificate property",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
					certificate: "expiredkey"
				}
			`,
			certificateOverride: "",
			expected:            "build/make/target/product/security/expiredkey.x509.pem build/make/target/product/security/expiredkey.pk8",
		},
		{
			name: "certificate overrides",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
					certificate: "expiredkey"
				}

				android_app_certificate {
					name: "new_certificate",
			    certificate: "cert/new_cert",
				}
			`,
			certificateOverride: "foo:new_certificate",
			expected:            "cert/new_cert.x509.pem cert/new_cert.pk8",
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(nil)
			if test.certificateOverride != "" {
				config.TestProductVariables.CertificateOverrides = []string{test.certificateOverride}
			}
			ctx := testAppContext(config, test.bp, nil)

			run(t, ctx, config)
			foo := ctx.ModuleForTests("foo", "android_common")

			signapk := foo.Output("foo.apk")
			signFlags := signapk.Args["certificates"]
			if test.expected != signFlags {
				t.Errorf("Incorrect signing flags, expected: %q, got: %q", test.expected, signFlags)
			}
		})
	}
}

func TestPackageNameOverride(t *testing.T) {
	testCases := []struct {
		name                string
		bp                  string
		packageNameOverride string
		expected            []string
	}{
		{
			name: "default",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
				}
			`,
			packageNameOverride: "",
			expected: []string{
				buildDir + "/.intermediates/foo/android_common/foo.apk",
				buildDir + "/target/product/test_device/system/app/foo/foo.apk",
			},
		},
		{
			name: "overridden",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
				}
			`,
			packageNameOverride: "foo:bar",
			expected: []string{
				// The package apk should be still be the original name for test dependencies.
				buildDir + "/.intermediates/foo/android_common/foo.apk",
				buildDir + "/target/product/test_device/system/app/bar/bar.apk",
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(nil)
			if test.packageNameOverride != "" {
				config.TestProductVariables.PackageNameOverrides = []string{test.packageNameOverride}
			}
			ctx := testAppContext(config, test.bp, nil)

			run(t, ctx, config)
			foo := ctx.ModuleForTests("foo", "android_common")

			outputs := foo.AllOutputs()
			outputMap := make(map[string]bool)
			for _, o := range outputs {
				outputMap[o] = true
			}
			for _, e := range test.expected {
				if _, exist := outputMap[e]; !exist {
					t.Errorf("Can't find %q in output files.\nAll outputs:%v", e, outputs)
				}
			}
		})
	}
}

func TestInstrumentationTargetOverridden(t *testing.T) {
	bp := `
		android_app {
			name: "foo",
			srcs: ["a.java"],
		}

		android_test {
			name: "bar",
			instrumentation_for: "foo",
		}
		`
	config := testConfig(nil)
	config.TestProductVariables.ManifestPackageNameOverrides = []string{"foo:org.dandroid.bp"}
	ctx := testAppContext(config, bp, nil)

	run(t, ctx, config)

	bar := ctx.ModuleForTests("bar", "android_common")
	res := bar.Output("package-res.apk")
	aapt2Flags := res.Args["flags"]
	e := "--rename-instrumentation-target-package org.dandroid.bp"
	if !strings.Contains(aapt2Flags, e) {
		t.Errorf("target package renaming flag, %q is missing in aapt2 link flags, %q", e, aapt2Flags)
	}
}

func TestOverrideAndroidApp(t *testing.T) {
	ctx := testJava(t, `
		android_app {
			name: "foo",
			srcs: ["a.java"],
			certificate: "expiredkey",
			overrides: ["qux"],
		}

		override_android_app {
			name: "bar",
			base: "foo",
			certificate: ":new_certificate",
		}

		android_app_certificate {
			name: "new_certificate",
			certificate: "cert/new_cert",
		}

		override_android_app {
			name: "baz",
			base: "foo",
			package_name: "org.dandroid.bp",
		}
		`)

	expectedVariants := []struct {
		moduleName  string
		variantName string
		apkName     string
		apkPath     string
		signFlag    string
		overrides   []string
		aaptFlag    string
	}{
		{
			moduleName:  "foo",
			variantName: "android_common",
			apkPath:     "/target/product/test_device/system/app/foo/foo.apk",
			signFlag:    "build/make/target/product/security/expiredkey.x509.pem build/make/target/product/security/expiredkey.pk8",
			overrides:   []string{"qux"},
			aaptFlag:    "",
		},
		{
			moduleName:  "bar",
			variantName: "android_common_bar",
			apkPath:     "/target/product/test_device/system/app/bar/bar.apk",
			signFlag:    "cert/new_cert.x509.pem cert/new_cert.pk8",
			overrides:   []string{"qux", "foo"},
			aaptFlag:    "",
		},
		{
			moduleName:  "baz",
			variantName: "android_common_baz",
			apkPath:     "/target/product/test_device/system/app/baz/baz.apk",
			signFlag:    "build/make/target/product/security/expiredkey.x509.pem build/make/target/product/security/expiredkey.pk8",
			overrides:   []string{"qux", "foo"},
			aaptFlag:    "--rename-manifest-package org.dandroid.bp",
		},
	}
	for _, expected := range expectedVariants {
		variant := ctx.ModuleForTests("foo", expected.variantName)

		// Check the final apk name
		outputs := variant.AllOutputs()
		expectedApkPath := buildDir + expected.apkPath
		found := false
		for _, o := range outputs {
			if o == expectedApkPath {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Can't find %q in output files.\nAll outputs:%v", expectedApkPath, outputs)
		}

		// Check the certificate paths
		signapk := variant.Output("foo.apk")
		signFlag := signapk.Args["certificates"]
		if expected.signFlag != signFlag {
			t.Errorf("Incorrect signing flags, expected: %q, got: %q", expected.signFlag, signFlag)
		}

		// Check if the overrides field values are correctly aggregated.
		mod := variant.Module().(*AndroidApp)
		if !reflect.DeepEqual(expected.overrides, mod.appProperties.Overrides) {
			t.Errorf("Incorrect overrides property value, expected: %q, got: %q",
				expected.overrides, mod.appProperties.Overrides)
		}

		// Check the package renaming flag, if exists.
		res := variant.Output("package-res.apk")
		aapt2Flags := res.Args["flags"]
		if !strings.Contains(aapt2Flags, expected.aaptFlag) {
			t.Errorf("package renaming flag, %q is missing in aapt2 link flags, %q", expected.aaptFlag, aapt2Flags)
		}
	}
}

func TestOverrideAndroidAppDependency(t *testing.T) {
	ctx := testJava(t, `
		android_app {
			name: "foo",
			srcs: ["a.java"],
		}

		override_android_app {
			name: "bar",
			base: "foo",
			package_name: "org.dandroid.bp",
		}

		android_test {
			name: "baz",
			srcs: ["b.java"],
			instrumentation_for: "foo",
		}

		android_test {
			name: "qux",
			srcs: ["b.java"],
			instrumentation_for: "bar",
		}
		`)

	// Verify baz, which depends on the overridden module foo, has the correct classpath javac arg.
	javac := ctx.ModuleForTests("baz", "android_common").Rule("javac")
	fooTurbine := filepath.Join(buildDir, ".intermediates", "foo", "android_common", "turbine-combined", "foo.jar")
	if !strings.Contains(javac.Args["classpath"], fooTurbine) {
		t.Errorf("baz classpath %v does not contain %q", javac.Args["classpath"], fooTurbine)
	}

	// Verify qux, which depends on the overriding module bar, has the correct classpath javac arg.
	javac = ctx.ModuleForTests("qux", "android_common").Rule("javac")
	barTurbine := filepath.Join(buildDir, ".intermediates", "foo", "android_common_bar", "turbine-combined", "foo.jar")
	if !strings.Contains(javac.Args["classpath"], barTurbine) {
		t.Errorf("qux classpath %v does not contain %q", javac.Args["classpath"], barTurbine)
	}
}

func TestAndroidAppImport(t *testing.T) {
	ctx := testJava(t, `
		android_app_import {
			name: "foo",
			apk: "prebuilts/apk/app.apk",
			certificate: "platform",
			dex_preopt: {
				enabled: true,
			},
		}
		`)

	variant := ctx.ModuleForTests("foo", "android_common")

	// Check dexpreopt outputs.
	if variant.MaybeOutput("dexpreopt/oat/arm64/package.vdex").Rule == nil ||
		variant.MaybeOutput("dexpreopt/oat/arm64/package.odex").Rule == nil {
		t.Errorf("can't find dexpreopt outputs")
	}

	// Check cert signing flag.
	signedApk := variant.Output("signed/foo.apk")
	signingFlag := signedApk.Args["certificates"]
	expected := "build/make/target/product/security/platform.x509.pem build/make/target/product/security/platform.pk8"
	if expected != signingFlag {
		t.Errorf("Incorrect signing flags, expected: %q, got: %q", expected, signingFlag)
	}
}

func TestAndroidAppImport_NoDexPreopt(t *testing.T) {
	ctx := testJava(t, `
		android_app_import {
			name: "foo",
			apk: "prebuilts/apk/app.apk",
			certificate: "platform",
			dex_preopt: {
				enabled: false,
			},
		}
		`)

	variant := ctx.ModuleForTests("foo", "android_common")

	// Check dexpreopt outputs. They shouldn't exist.
	if variant.MaybeOutput("dexpreopt/oat/arm64/package.vdex").Rule != nil ||
		variant.MaybeOutput("dexpreopt/oat/arm64/package.odex").Rule != nil {
		t.Errorf("dexpreopt shouldn't have run.")
	}
}

func TestAndroidAppImport_Presigned(t *testing.T) {
	ctx := testJava(t, `
		android_app_import {
			name: "foo",
			apk: "prebuilts/apk/app.apk",
			presigned: true,
			dex_preopt: {
				enabled: true,
			},
		}
		`)

	variant := ctx.ModuleForTests("foo", "android_common")

	// Check dexpreopt outputs.
	if variant.MaybeOutput("dexpreopt/oat/arm64/package.vdex").Rule == nil ||
		variant.MaybeOutput("dexpreopt/oat/arm64/package.odex").Rule == nil {
		t.Errorf("can't find dexpreopt outputs")
	}
	// Make sure stripping wasn't done.
	stripRule := variant.Output("dexpreopt/foo.apk")
	if !strings.HasPrefix(stripRule.RuleParams.Command, "cp -f") {
		t.Errorf("unexpected, non-skipping strip command: %q", stripRule.RuleParams.Command)
	}

	// Make sure signing was skipped and aligning was done instead.
	if variant.MaybeOutput("signed/foo.apk").Rule != nil {
		t.Errorf("signing rule shouldn't be included.")
	}
	if variant.MaybeOutput("zip-aligned/foo.apk").Rule == nil {
		t.Errorf("can't find aligning rule")
	}
}

func TestAndroidAppImport_DpiVariants(t *testing.T) {
	bp := `
		android_app_import {
			name: "foo",
			apk: "prebuilts/apk/app.apk",
			dpi_variants: {
				xhdpi: {
					apk: "prebuilts/apk/app_xhdpi.apk",
				},
				xxhdpi: {
					apk: "prebuilts/apk/app_xxhdpi.apk",
				},
			},
			certificate: "PRESIGNED",
			dex_preopt: {
				enabled: true,
			},
		}
		`
	testCases := []struct {
		name                string
		aaptPreferredConfig *string
		aaptPrebuiltDPI     []string
		expected            string
	}{
		{
			name:                "no preferred",
			aaptPreferredConfig: nil,
			aaptPrebuiltDPI:     []string{},
			expected:            "prebuilts/apk/app.apk",
		},
		{
			name:                "AAPTPreferredConfig matches",
			aaptPreferredConfig: proptools.StringPtr("xhdpi"),
			aaptPrebuiltDPI:     []string{"xxhdpi", "lhdpi"},
			expected:            "prebuilts/apk/app_xhdpi.apk",
		},
		{
			name:                "AAPTPrebuiltDPI matches",
			aaptPreferredConfig: proptools.StringPtr("mdpi"),
			aaptPrebuiltDPI:     []string{"xxhdpi", "xhdpi"},
			expected:            "prebuilts/apk/app_xxhdpi.apk",
		},
		{
			name:                "non-first AAPTPrebuiltDPI matches",
			aaptPreferredConfig: proptools.StringPtr("mdpi"),
			aaptPrebuiltDPI:     []string{"ldpi", "xhdpi"},
			expected:            "prebuilts/apk/app_xhdpi.apk",
		},
		{
			name:                "no matches",
			aaptPreferredConfig: proptools.StringPtr("mdpi"),
			aaptPrebuiltDPI:     []string{"ldpi", "xxxhdpi"},
			expected:            "prebuilts/apk/app.apk",
		},
	}

	jniRuleRe := regexp.MustCompile("^if \\(zipinfo (\\S+)")
	for _, test := range testCases {
		config := testConfig(nil)
		config.TestProductVariables.AAPTPreferredConfig = test.aaptPreferredConfig
		config.TestProductVariables.AAPTPrebuiltDPI = test.aaptPrebuiltDPI
		ctx := testAppContext(config, bp, nil)

		run(t, ctx, config)

		variant := ctx.ModuleForTests("foo", "android_common")
		jniRuleCommand := variant.Output("jnis-uncompressed/foo.apk").RuleParams.Command
		matches := jniRuleRe.FindStringSubmatch(jniRuleCommand)
		if len(matches) != 2 {
			t.Errorf("failed to extract the src apk path from %q", jniRuleCommand)
		}
		if test.expected != matches[1] {
			t.Errorf("wrong src apk, expected: %q got: %q", test.expected, matches[1])
		}
	}
}

func TestStl(t *testing.T) {
	ctx := testJava(t, cc.GatherRequiredDepsForTest(android.Android)+`
		cc_library {
			name: "libjni",
		}

		android_test {
			name: "stl",
			jni_libs: ["libjni"],
			compile_multilib: "both",
			sdk_version: "current",
			stl: "c++_shared",
		}

		android_test {
			name: "system",
			jni_libs: ["libjni"],
			compile_multilib: "both",
			sdk_version: "current",
		}

		ndk_prebuilt_shared_stl {
			name: "ndk_libc++_shared",
		}
		`)

	testCases := []struct {
		name string
		jnis []string
	}{
		{"stl",
			[]string{
				"libjni.so",
				"libc++_shared.so",
			},
		},
		{"system",
			[]string{
				"libjni.so",
			},
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			app := ctx.ModuleForTests(test.name, "android_common")
			jniLibZip := app.Output("jnilibs.zip")
			var jnis []string
			args := strings.Fields(jniLibZip.Args["jarArgs"])
			for i := 0; i < len(args); i++ {
				if args[i] == "-f" {
					jnis = append(jnis, args[i+1])
					i += 1
				}
			}
			jnisJoined := strings.Join(jnis, " ")
			for _, jni := range test.jnis {
				if !strings.Contains(jnisJoined, jni) {
					t.Errorf("missing jni %q in %q", jni, jnis)
				}
			}
		})
	}
}

func TestUsesLibraries(t *testing.T) {
	bp := `
		java_sdk_library {
			name: "foo",
			srcs: ["a.java"],
			api_packages: ["foo"],
		}

		java_sdk_library {
			name: "bar",
			srcs: ["a.java"],
			api_packages: ["bar"],
		}

		android_app {
			name: "app",
			srcs: ["a.java"],
			uses_libs: ["foo"],
			optional_uses_libs: [
				"bar",
				"baz",
			],
		}

		android_app_import {
			name: "prebuilt",
			apk: "prebuilts/apk/app.apk",
			certificate: "platform",
			uses_libs: ["foo"],
			optional_uses_libs: [
				"bar",
				"baz",
			],
		}
	`

	config := testConfig(nil)
	config.TestProductVariables.MissingUsesLibraries = []string{"baz"}

	ctx := testAppContext(config, bp, nil)

	run(t, ctx, config)

	app := ctx.ModuleForTests("app", "android_common")
	prebuilt := ctx.ModuleForTests("prebuilt", "android_common")

	// Test that all libraries are verified
	cmd := app.Rule("verify_uses_libraries").RuleParams.Command
	if w := "--uses-library foo"; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}

	if w := "--optional-uses-library bar --optional-uses-library baz"; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}

	cmd = prebuilt.Rule("verify_uses_libraries").RuleParams.Command

	if w := `uses_library_names="foo"`; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}

	if w := `optional_uses_library_names="bar baz"`; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}

	// Test that only present libraries are preopted
	cmd = app.Rule("dexpreopt").RuleParams.Command

	if w := `dex_preopt_target_libraries="/system/framework/foo.jar /system/framework/bar.jar"`; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}

	cmd = prebuilt.Rule("dexpreopt").RuleParams.Command

	if w := `dex_preopt_target_libraries="/system/framework/foo.jar /system/framework/bar.jar"`; !strings.Contains(cmd, w) {
		t.Errorf("wanted %q in %q", w, cmd)
	}
}

func TestCodelessApp(t *testing.T) {
	testCases := []struct {
		name   string
		bp     string
		noCode bool
	}{
		{
			name: "normal",
			bp: `
				android_app {
					name: "foo",
					srcs: ["a.java"],
				}
			`,
			noCode: false,
		},
		{
			name: "app without sources",
			bp: `
				android_app {
					name: "foo",
				}
			`,
			noCode: true,
		},
		{
			name: "app with libraries",
			bp: `
				android_app {
					name: "foo",
					static_libs: ["lib"],
				}

				java_library {
					name: "lib",
					srcs: ["a.java"],
				}
			`,
			noCode: false,
		},
		{
			name: "app with sourceless libraries",
			bp: `
				android_app {
					name: "foo",
					static_libs: ["lib"],
				}

				java_library {
					name: "lib",
				}
			`,
			// TODO(jungjw): this should probably be true
			noCode: false,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			ctx := testApp(t, test.bp)

			foo := ctx.ModuleForTests("foo", "android_common")
			manifestFixerArgs := foo.Output("manifest_fixer/AndroidManifest.xml").Args["args"]
			if strings.Contains(manifestFixerArgs, "--has-no-code") != test.noCode {
				t.Errorf("unexpected manifest_fixer args: %q", manifestFixerArgs)
			}
		})
	}
}
