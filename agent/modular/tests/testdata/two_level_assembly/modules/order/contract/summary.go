package contract

import "context"

// Summarizer 是装配测试用的能力，按客户信息构造订单摘要。
type Summarizer interface {
	Summary(context.Context, string) (string, error)
}
