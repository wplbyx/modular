package contract

import "context"

// Reader 在资源就绪后查询客户名称；依赖故障原样返回。
type Reader interface {
	Name(context.Context, string) (string, error)
}
