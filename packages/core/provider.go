package core

// Provider 为调用方提供一个已初始化的依赖。
//
// Provider 不负责触发生命周期；依赖必须先由 Application 完成 Setup。
type Provider[T any] interface {
	Value() (T, error)
}

// ProviderFunc 将函数适配为 Provider。
type ProviderFunc[T any] func() (T, error)

// Value 返回函数提供的值。
func (f ProviderFunc[T]) Value() (T, error) {
	return f()
}
