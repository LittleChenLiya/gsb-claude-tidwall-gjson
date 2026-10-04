package main

import (
	"fmt"
	"github.com/tidwall/gjson"
)

func main() {
	json := `{"users":[{"name":"Alice","active":true},{"name":"Bob","active":false},{"name":"Carol","active":true}]}`
	hits := gjson.Report(json, "users.#(active==true)#.name")
	fmt.Printf("Report: users.#(active==true)#.name\n")
	fmt.Printf("JSON:   %s\n\n", json)
	fmt.Printf("Found %d hit(s):\n", len(hits))
	for i, h := range hits {
		fmt.Printf("  hit %d:\n", i+1)
		fmt.Printf("    path:  %s\n", h.Path)
		fmt.Printf("    index: %d\n", h.Index)
		fmt.Printf("    end:   %d\n", h.End)
		fmt.Printf("    raw:   %s\n", h.Result.Raw)
		// Round-trip: query the path back and verify it matches.
		rt := gjson.Get(json, h.Path)
		fmt.Printf("    round-trip: Get(json, %q) = %q (match: %v)\n", h.Path, rt.Raw, rt.Raw == h.Result.Raw)
	}
}
