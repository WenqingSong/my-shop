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
