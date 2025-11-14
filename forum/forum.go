package forum

import "net/http"

func forumHandler(w http.ResponseWriter, r *http.Request) {

}

func threadHandler(w http.ResponseWriter, r *http.Request) {

}

func postHandler(w http.ResponseWriter, r *http.Request) {

}

func Routers(mux *http.ServeMux) {
	mux.HandleFunc("/forum", forumHandler)
	mux.HandleFunc("/forum/thread", threadHandler)
	mux.HandleFunc("/forum/post", postHandler)
}
