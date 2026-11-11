// Package builtin 注册平台内置的部署方式。
package builtin

import (
	"deployhub/internal/driver"
	"deployhub/internal/driver/gobinary"
	"deployhub/internal/driver/image"
	"deployhub/internal/driver/static"
	"deployhub/internal/driver/workers"
)

// Registry 返回内置部署方式的注册表。
func Registry() (*driver.Registry, error) {
	return driver.NewRegistry(
		gobinary.Driver{},
		static.Driver{},
		image.Driver{},
		workers.Driver{},
	)
}
