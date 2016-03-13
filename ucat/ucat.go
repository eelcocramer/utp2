package main

import (
	"fmt"

	"github.com/h2so5/utp2"
)

func main() {
	addr, err := utp.ResolveAddr("utp", "")
	listener, err := utp.Listen("utp", addr)
	fmt.Print(listener, err)
}
