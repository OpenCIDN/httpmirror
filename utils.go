package httpmirror

import (
	"strings"
)

func cleanPath(path string) string {
	ps := strings.Split(path, "/")
	var out []string
	for _, p := range ps {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, p)
	}

	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}
