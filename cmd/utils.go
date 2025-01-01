package main

import "strconv"

func size(s string) int64 {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	} else {
		panic(err)
	}
}
