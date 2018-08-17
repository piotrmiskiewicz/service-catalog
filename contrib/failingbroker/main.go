package main

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/v2/catalog", getCatalog)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getCatalog(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusInternalServerError)
}
