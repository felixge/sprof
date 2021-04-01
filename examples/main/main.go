package main

import (
	"github.com/felixge/sprof/examples/main/mypkg"
)

func main() {
	funcs()
	var c calls
	if true {
		c.execution()
		c.invocation()
	}
	funcs()
}

func funcs() {
	mypkg.Exported()
}

type calls struct{}

func (calls) execution() {
	mypkg.Regular()
}

func (calls) invocation() {
	mypkg.T.Static()
	var i mypkg.Iface = mypkg.T
	i.Dynamic()
}
