package main

import (
	"fmt"
	"runtime"

	"github.com/h2so5/utp2"
)

func main() {
	numcpu := runtime.NumCPU()
	runtime.GOMAXPROCS(numcpu)

	addr, err := utp.ResolveAddr("utp", "")
	listener, err := utp.Listen("utp", addr)
	fmt.Println(listener.RawConn.LocalAddr(), err)

	rb := utp.NewRingBuffer(3, uint16(65534))
	fmt.Println(rb.Push([]byte("xxxx")))
	fmt.Println(rb.Push([]byte("xxxx")))
	fmt.Println(rb.Push([]byte("xxxx")))
	rb.Erase(65535)
	fmt.Println(rb.Pop())
	fmt.Println(rb.Push([]byte("xxxx")))
	fmt.Println(rb.Push([]byte("xxxx")))
	fmt.Println(rb.Push([]byte("xxxx")))

	c, _ := listener.AcceptUTP()
	var buf [256]byte
	for {
		l, _ := c.Read(buf[:])
		c.Write(buf[:l])
		fmt.Println(buf[:l])
	}
}
