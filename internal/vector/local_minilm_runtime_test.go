//go:build linux && tagmem_onnx

package vector

import "testing"

func TestORTRuntimeSpecForPlatform(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		goarch  string
		useGPU  bool
		wantURL string
		wantLib string
		wantErr bool
	}{
		{name: "linux amd64 cpu", goos: "linux", goarch: "amd64", wantURL: ortLinuxAMD64CPUURL, wantLib: ortLibraryLinuxAMD64},
		{name: "linux amd64 gpu", goos: "linux", goarch: "amd64", useGPU: true, wantURL: ortLinuxAMD64GPUURL, wantLib: ortLibraryLinuxAMD64},
		{name: "linux arm64 cpu", goos: "linux", goarch: "arm64", wantURL: ortLinuxARM64CPUURL, wantLib: ortLibraryLinuxARM64},
		{name: "linux arm64 gpu unsupported", goos: "linux", goarch: "arm64", useGPU: true, wantErr: true},
		{name: "darwin arm64 unsupported", goos: "darwin", goarch: "arm64", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ortRuntimeSpecForPlatform(tc.goos, tc.goarch, tc.useGPU)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ortRuntimeSpecForPlatform(%q, %q, %t) error = nil, want error", tc.goos, tc.goarch, tc.useGPU)
				}
				return
			}
			if err != nil {
				t.Fatalf("ortRuntimeSpecForPlatform(%q, %q, %t) error = %v", tc.goos, tc.goarch, tc.useGPU, err)
			}
			if spec.url != tc.wantURL {
				t.Fatalf("url = %q, want %q", spec.url, tc.wantURL)
			}
			if spec.libraryName != tc.wantLib {
				t.Fatalf("libraryName = %q, want %q", spec.libraryName, tc.wantLib)
			}
		})
	}
}
