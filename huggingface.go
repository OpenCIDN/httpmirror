package httpmirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	hfHosts = map[string]struct{}{
		"huggingface.co": {},
		"hf-mirror.com":  {},
	}
)

func (m *MirrorHandler) setHuggingFaceHeaders(rw http.ResponseWriter, r *http.Request) error {
	// Special handling for huggingface.co to add X-Repo-Commit header with HF_ENDPOINT
	if m.RemoteCache == nil {
		return nil
	}

	if _, ok := hfHosts[r.Host]; !ok {
		return nil
	}

	rIndex := strings.Index(r.URL.Path, "/resolve/")
	if rIndex < 0 {
		return nil
	}

	repoRef := r.URL.Path[rIndex+9:]
	slashIndex := strings.Index(repoRef, "/")
	if slashIndex >= 0 {
		repoRef = repoRef[:slashIndex]
	}

	if len(repoRef) == 40 {
		rw.Header().Set("X-Repo-Commit", repoRef)
		return nil
	}

	repoName := r.URL.Path[1:rIndex]
	repoType := "models"
	if strings.HasPrefix(repoName, "datasets/") {
		repoType = "datasets"
		repoName = strings.TrimPrefix(repoName, "datasets/")
	} else if strings.HasPrefix(repoName, "spaces/") {
		repoType = "spaces"
		repoName = strings.TrimPrefix(repoName, "spaces/")
	}

	file := fmt.Sprintf(r.Host+"/api/%s/%s/revision/%s", repoType, repoName, repoRef)
	if m.Logger != nil {
		m.Logger.Println("HF Repo Info", file)
	}

	ctx := r.Context()

	setFromCache := func() {
		fr, err := m.RemoteCache.Reader(ctx, file)
		if err != nil {
			if m.Logger != nil {
				m.Logger.Println("HF Repo Reader error", file, err)
			}
			return
		}
		defer fr.Close()

		var sha struct {
			Sha string `json:"sha"`
		}

		_ = json.NewDecoder(fr).Decode(&sha)
		if sha.Sha != "" {
			rw.Header().Set("X-Repo-Commit", sha.Sha)
		}
	}

	cacheInfo, err := m.RemoteCache.Stat(ctx, file)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		if m.Logger != nil {
			m.Logger.Println("HF Cache Miss", file, err)
		}
	} else {
		if m.Logger != nil {
			m.Logger.Println("HF Cache Hit", file)
		}

		if m.CIDNClient == nil {
			sourceCtx, sourceCancel := context.WithTimeout(ctx, m.CheckSyncTimeout)
			sourceInfo, err := httpHead(sourceCtx, m.client(), r.URL.String())
			if err != nil {
				sourceCancel()
				if m.Logger != nil {
					m.Logger.Println("HF Source Miss", file, err)
				}
				setFromCache()
				return nil
			}
			sourceCancel()

			sourceSize := sourceInfo.Size()
			cacheSize := cacheInfo.Size()
			if cacheSize != 0 && (sourceSize <= 0 || sourceSize == cacheSize) {
				setFromCache()
				return nil
			}

			if m.Logger != nil {
				m.Logger.Println("HF Source change", file, sourceSize, cacheSize)
			}
		}

	}

	ch := m.group.DoChan(file, func() (interface{}, error) {
		url := "https://" + file
		return nil, m.cacheFile(context.Background(), url, file)
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			if cacheInfo != nil {
				if m.Logger != nil {
					m.Logger.Println("HF Recache error", file, result.Err)
				}
				setFromCache()
				return nil
			}

			if errors.Is(result.Err, ErrNotOK) {
				return nil
			}
			return result.Err
		}
		setFromCache()
	}

	return nil
}
