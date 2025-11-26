package forum

// mdToHTML is a helper function that converts markdown to HTML
var mdToHTML func([]byte) []byte

// SetMdToHTML sets the mdToHTML function reference from main
func SetMdToHTML(fn func([]byte) []byte) {
	mdToHTML = fn
}
