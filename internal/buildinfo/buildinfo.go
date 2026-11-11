// Package buildinfo 保存编译时注入的版本信息。
package buildinfo

// Version 由 Makefile 通过 -ldflags "-X deployhub/internal/buildinfo.Version=..." 注入。
var Version = "dev"
