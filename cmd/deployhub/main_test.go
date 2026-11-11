package main

import "testing"

func TestCheckListen(t *testing.T) {
	// 本机地址可以不填对外地址
	for _, a := range []string{"127.0.0.1:7788", "localhost:7788", "[::1]:7788", "127.0.0.2:80"} {
		if _, err := checkListen(a, ""); err != nil {
			t.Errorf("%s 应该允许：%v", a, err)
		}
	}
	// 不是本机地址时必须填对外地址；地址本身无效一律拒绝
	for _, a := range []string{"0.0.0.0:7788", ":7788", "192.168.1.2:7788", "example.com:7788", "127.0.0.1", "127.0.0.1:"} {
		if _, err := checkListen(a, ""); err == nil {
			t.Errorf("%s 没有对外地址时应该拒绝", a)
		}
	}
	got, err := checkListen("0.0.0.0:7788", " https://deploy.example.com/ ")
	if err != nil || got != "https://deploy.example.com" {
		t.Fatalf("checkListen = %q %v", got, err)
	}
	for _, u := range []string{"deploy.example.com", "ftp://x", "https://deploy.example.com/hub", "https://deploy.example.com?a=1"} {
		if _, err := checkListen("0.0.0.0:7788", u); err == nil {
			t.Errorf("对外地址 %s 应该拒绝", u)
		}
	}
}

func TestExpandHome(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	got, err := expandHome("~/.deployhub")
	if err != nil || got != "/home/test/.deployhub" {
		t.Fatalf("expandHome = %q %v", got, err)
	}
}
