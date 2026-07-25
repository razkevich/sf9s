// mockorgd runs the e2e mock org server standalone — used to produce demo
// recordings and screenshots without touching a real org.
package main

import (
	"fmt"

	"github.com/razkevich/sf9s/e2e/mockorg"
)

func main() {
	s := mockorg.New()
	fmt.Println(s.URL)
	select {}
}
