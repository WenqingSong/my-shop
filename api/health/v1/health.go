package v1

import "github.com/gogf/gf/v2/frame/g"

// LivenessReq is the request of the liveness probe.
type LivenessReq struct {
	g.Meta `path:"/health" method:"get" tags:"Health" summary:"Liveness probe"`
}

// LivenessRes is the response of the liveness probe.
type LivenessRes struct {
	Status string `json:"status" dc:"service liveness status, always \"ok\" when alive"`
	Time   string `json:"time"   dc:"current server time"`
}

// ReadinessReq is the request of the readiness probe.
type ReadinessReq struct {
	g.Meta `path:"/ready" method:"get" tags:"Health" summary:"Readiness probe"`
}

// ReadinessRes is the response of the readiness probe.
type ReadinessRes struct {
	Status string `json:"status" dc:"service readiness status, \"ready\" when DB and Redis are reachable"`
}
