package memlimit

import (
	"io/fs"
	"testing"
)

func files(contents map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := contents[path]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return []byte(content), nil
	}
}

func TestFromCgroup(t *testing.T) {
	for _, test := range []struct {
		name      string
		explicit  string
		files     map[string]string
		wantLimit int64
		wantOK    bool
	}{
		{"cgroup v2 limit", "", map[string]string{cgroupV2Max: "402653184\n"}, 402653184 / 100 * 85, true},
		{"cgroup v1 limit", "", map[string]string{cgroupV1Max: "402653184\n"}, 402653184 / 100 * 85, true},
		{"v2 is read before v1", "", map[string]string{cgroupV2Max: "200\n", cgroupV1Max: "400\n"}, 170, true},
		{"explicit GOMEMLIMIT wins", "300MiB", map[string]string{cgroupV2Max: "402653184\n"}, 0, false},
		{"v2 without a limit", "", map[string]string{cgroupV2Max: "max\n"}, 0, false},
		{"v1 without a limit", "", map[string]string{cgroupV1Max: "9223372036854771712\n"}, 0, false},
		{"not in a cgroup", "", map[string]string{}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit, ok := FromCgroup(test.explicit, files(test.files))
			if limit != test.wantLimit || ok != test.wantOK {
				t.Fatalf("FromCgroup() = %d, %v; want %d, %v", limit, ok, test.wantLimit, test.wantOK)
			}
		})
	}
}
