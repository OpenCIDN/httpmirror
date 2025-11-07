package httpmirror

import "testing"

func Test_cleanPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "empty path",
			path: "",
			want: "/",
		},
		{
			name: "already clean path",
			path: "/a/b/c",
			want: "/a/b/c",
		},
		{
			name: "path with redundant slashes",
			path: "/a//b///c",
			want: "/a/b/c",
		},
		{
			name: "path with trailing slash",
			path: "/a/b/c/",
			want: "/a/b/c",
		},
		{
			name: "path with dot segments",
			path: "/a/./b/../c",
			want: "/a/c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanPath(tt.path)
			if got != tt.want {
				t.Errorf("cleanPath() = %v, want %v", got, tt.want)
			}
		})
	}
}
