// Command qiniu-mock 是 T2-04 容器生命周期验证专用的七牛 UC / bucketInfo Mock。
//
// 背景：生产二进制启动时必须对七牛 bucket 做真实只读可用性检查（GetBucketInfo），
// 该检查需要真实凭据与外网。容器级 SIGTERM / HTTP drain 验证不能因此跳过启动链路，
// 否则验证对象就不是真实服务进程。
//
// 本 Mock 只替换网络终点，不替换校验逻辑：应用仍由七牛 SDK 完成签名、请求与响应解析。
// 该二进制仅用于 scripts/test-container-runtime.sh，不进入生产镜像。
// 与进程级测试（test/e2e/serve_lifecycle_harness_test.go）使用同一套响应语义。
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	qmeta "github.com/qiniu/go-sdk/v7/storagev2/apis/query_bucket_v4"
)

func main() {
	addr := envOr("QINIU_MOCK_ADDR", "127.0.0.1:9443")
	certFile := os.Getenv("QINIU_MOCK_CERT")
	keyFile := os.Getenv("QINIU_MOCK_KEY")
	if certFile == "" || keyFile == "" {
		log.Fatal("qiniu-mock: 必须设置 QINIU_MOCK_CERT / QINIU_MOCK_KEY")
	}

	ucHost := "https://" + addr
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v2/bucketInfo"):
			log.Printf("qiniu_mock_request path=%s bucket=%s", r.URL.Path, r.URL.Query().Get("bucket"))
			// SDK 客户端会校验 X-Reqid（抗劫持），响应必须携带该头。
			w.Header().Set("X-Reqid", "t204-container-mock-reqid")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ctime":"2026-01-01T00:00:00Z","region":"z0"}`))
		case strings.Contains(r.URL.Path, "/v4/query"):
			log.Printf("qiniu_mock_request path=%s bucket=%s", r.URL.Path, r.URL.Query().Get("bucket"))
			resp := qmeta.Response{Hosts: qmeta.BucketQueryHosts{{
				RegionId:   "z0",
				TimeToLive: 3600,
				UpDomains: qmeta.UpDomains{
					AcceleratedUpDomains: []string{ucHost},
					PreferedUpDomains:    []string{ucHost},
					AlternativeUpDomains: []string{ucHost},
				},
				IoDomains:  qmeta.IoDomains{PreferedIoDomains: []string{ucHost}},
				RsDomains:  qmeta.RsDomains{PreferedRsDomains: []string{ucHost}},
				RsfDomains: qmeta.RsfDomains{PreferedRsfDomains: []string{ucHost}},
				ApiDomains: qmeta.ApiDomains{PreferedApiDomains: []string{ucHost}},
				S3Domains:  qmeta.S3Domains{RegionAlias: "z0", PreferedApiDomains: []string{ucHost}},
			}}}
			body, err := json.Marshal(&resp)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Reqid", "t204-container-mock-reqid")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		default:
			log.Printf("qiniu_mock_request path=%s status=404", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = srv.Close()
	}()

	fmt.Printf("qiniu-mock listening on %s (uc=%s)\n", addr, ucHost)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
		log.Fatalf("qiniu-mock: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
