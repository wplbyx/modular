// Package assemblycheck 验证应用只需公开装配接口即可连接模块。
package assemblycheck

import (
	"github.com/wplbyx/modular/packages/core"

	"shop/modules/customer"
	"shop/modules/order"
)

func assemble(names core.Provider[map[string]string]) (*order.Module, error) {
	customers, err := customer.New(customer.Config{}, customer.Dependencies{Names: names})
	if err != nil {
		return nil, err
	}
	return order.New(order.Config{}, order.Dependencies{Customers: customers.Reader})
}
