package util

import "stash.cloudflare.com/go-stream/stream/mapper"
import "stash.cloudflare.com/go-stream/stream"

func NewDropOp() *mapper.Op {
	dropfn := func(input stream.Object, out chan<- stream.Object) int {
		return 0
	}

	return mapper.NewOp(dropfn, "DropRop")
}

func NewMakeInterfaceOp() *mapper.Op {
	fn := func(in interface{}) []interface{} {
		return []interface{}{in}
	}

	return mapper.NewOp(fn, "MakeInterfaceOp")
}
