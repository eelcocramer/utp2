package main

import (
	"fmt"
	"runtime"

	"github.com/h2so5/utp2"
)

type te struct{}

func (t *te) index() int {
	return 999
}

func main() {
	numcpu := runtime.NumCPU()
	runtime.GOMAXPROCS(numcpu)

	addr, err := utp.ResolveAddr("utp", "")
	listener, err := utp.Listen("utp", addr)
	fmt.Println(listener.RawConn.LocalAddr(), err)

	listener.AcceptUTP()
	for {
	}
}
