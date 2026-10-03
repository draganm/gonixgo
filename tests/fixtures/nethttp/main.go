// Command nethttp is a gonixgo integration fixture: it uses the parts of
// the standard library that are built with cgo by default (net, os/user).
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/user"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "pong")
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	u, err := user.Current()
	fmt.Println(string(body), err == nil && u.Username != "")
}
