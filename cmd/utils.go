package main

import "strconv"

func size(s string) int64 {
	// Sizes are stored as strings in JSON; parsing failures are programmer errors here.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	} else {
		panic(err)
	}
}
