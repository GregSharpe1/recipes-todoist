package main

import (
	"log"

	"todoist-recipes/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
