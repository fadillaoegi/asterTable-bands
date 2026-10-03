package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
	_ "time/tzdata"

	"tablekeeper/internal/app"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://postgres@/tablekeeper?host=/var/run/postgresql"
	}
	a, err := app.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: "0.0.0.0:" + port, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
