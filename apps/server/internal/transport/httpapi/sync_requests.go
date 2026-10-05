package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"tripfolio/server/internal/foundation/apperr"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func syncRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sync/push" {
			next.ServeHTTP(w, r)
			return
		}
		if encoding := strings.TrimSpace(r.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
			WriteProblem(w, r, 415, "UNSUPPORTED_CONTENT_ENCODING", "同步请求不支持压缩正文")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, syncmodule.MaxPushBytes))
		if err != nil {
			WriteProblem(w, r, 413, "REQUEST_TOO_LARGE", "正文超过1 MiB或读取失败")
			return
		}
		if _, err = syncmodule.DecodePush(body); err != nil {
			if e, ok := apperr.As(err); ok {
				writeAppError(w, r, e)
			} else {
				requestError(w, r, err)
			}
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}
