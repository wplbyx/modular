package main

import (
	"github.com/wplbyx/modular/packages/generate/internal/contractport"
	"google.golang.org/protobuf/compiler/protogen"
)

func main() {
	protogen.Options{}.Run(contractport.Generate)
}
