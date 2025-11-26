package forum

import (
	"fmt"
	"net/http"
	"strings"

	"edev/auth"
	"edev/config"
	"edev/db"
	"edev/log"
	"edev/templates"
	"edev/utils"
)

// Routes registers all forum-related routes with the provided ServeMux.
func Routes(mux *http.ServeMux) {
	mux.HandleFunc("/forum", listHandler)                                                 // list all forums
	mux.HandleFunc("GET /forum/create", createHandler)                                    // show create forum form
	mux.HandleFunc("POST /forum/create", createPostHandler)                               // create new forum
	mux.HandleFunc("/forum/{forumID}", viewHandler)                                       // view single forum and threads
	mux.HandleFunc("GET /forum/{forumID}/edit", editHandler)                              // show edit forum form
	mux.HandleFunc("POST /forum/{forumID}/edit", editPostHandler)                         // save forum changes
	mux.HandleFunc("/forum/{forumID}/thread/{threadID}", threadViewHandler)               // view thread and posts
	mux.HandleFunc("POST /forum/{forumID}/thread", createThreadHandler)                   // create new thread
	mux.HandleFunc("GET /forum/{forumID}/thread/{threadID}/edit", threadEditHandler)      // show edit thread form
	mux.HandleFunc("POST /forum/{forumID}/thread/{threadID}/edit", threadEditPostHandler) // save thread changes
	mux.HandleFunc("POST /forum/{forumID}/thread/{threadID}/post", createPostHandler)     // create post
	mux.HandleFunc("PATCH /post/{postID}", editPostHandler)                               // edit post
	mux.HandleFunc("DELETE /post/{postID}", deletePostHandler)                            // delete post
}

// listHandler handles GET /forum - list all forums
func listHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	limit := 20
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	log.Printf("serving forum list for user %s", u.Email)

	// Fetch forums from database
	forums, err := db.Storage.ListForums(offset, limit)
	if err != nil {
		log.Printf("forums lookup error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	hasMore := len(forums) >= limit
	nextOffset := offset + limit

	data := struct {
		Authed     bool
		User       db.User
		Config     config.Config
		Forums     []*db.Forum
		Limit      int
		NextOffset int
		HasMore    bool
	}{
		Authed:     true,
		User:       *u,
		Config:     *config.Cfg,
		Forums:     forums,
		Limit:      limit,
		NextOffset: nextOffset,
		HasMore:    hasMore,
	}

	if err := templates.ExecuteTemplate(w, "forum.go.tmpl", data); err != nil {
		log.Printf("template %s execute error: %v", "forum.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// viewHandler handles GET /forum/{forumID} - view single forum and threads
func viewHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	forumExtID := r.PathValue("forumID")

	log.Printf("serving forum view for forum %s, user %s", forumExtID, u.Email)

	// Fetch forum by external ID
	forumObj, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		if err == db.ErrNoRows {
			log.Printf("ERROR: forum not found for external_id=%s", forumExtID)
			http.Error(w, "forum not found", http.StatusNotFound)
			return
		}
		log.Printf("ERROR: forum lookup error for external_id=%s: %v", forumExtID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("DEBUG: forum loaded - id=%d, external_id=%s, title=%s", forumObj.ID, forumObj.ExternalID, forumObj.Title)

	// Fetch threads for this forum
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	const limit = 20
	threads, err := db.Storage.ListThreadsByForumID(forumObj.ID, offset, limit)
	if err != nil {
		log.Printf("ERROR: threads lookup error for forumID=%d: %v", forumObj.ID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	log.Printf("DEBUG: loaded %d threads for forumID=%d", len(threads), forumObj.ID)

	hasMore := len(threads) >= limit
	nextOffset := offset + limit

	data := struct {
		Authed     bool
		User       db.User
		Config     config.Config
		Forum      *db.Forum
		Threads    []*db.Thread
		Limit      int
		NextOffset int
		HasMore    bool
	}{
		Authed:     true,
		User:       *u,
		Config:     *config.Cfg,
		Forum:      forumObj,
		Threads:    threads,
		Limit:      limit,
		NextOffset: nextOffset,
		HasMore:    hasMore,
	}

	log.Printf("DEBUG: about to render forum_view.go.tmpl with Authed=%v, Forum.ID=%d, Threads=%d, HasMore=%v", data.Authed, data.Forum.ID, len(data.Threads), data.HasMore)

	if err := templates.ExecuteTemplate(w, "forum_view.go.tmpl", data); err != nil {
		log.Printf("ERROR: template %s execute error: %v", "forum_view.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// createHandler handles GET /forum/create - show create forum form
func createHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	// Render forum creation form
	data := struct {
		Authed  bool
		User    db.User
		Config  *config.Config
		Error   string
		Message string
	}{
		Authed:  authed,
		User:    *u,
		Config:  config.Cfg,
		Error:   "",
		Message: "",
	}

	if err := templates.ExecuteTemplate(w, "forum_create.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// createPostHandler handles POST /forum/create and POST /forum/{forumID}/thread/{threadID}/post
// This function is overloaded to handle both forum creation POST and post creation POST
// based on the route pattern.
func createPostHandler(w http.ResponseWriter, r *http.Request) {
	// Differentiate based on the route pattern
	if strings.Contains(r.RequestURI, "/thread/") && strings.HasSuffix(r.RequestURI, "/post") {
		// This is a post creation in a thread
		createPostInThreadHandler(w, r)
		return
	}

	// Otherwise it's forum creation
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	// Parse form data
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	imageURL := strings.TrimSpace(r.FormValue("imageURL"))

	// Validate title
	if title == "" {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Error:   "Título é obrigatório",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_create.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	if len(title) < 3 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Error:   "Título deve ter pelo menos 3 caracteres",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_create.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	if len(title) > 255 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Error:   "Título não pode ultrapassar 255 caracteres",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_create.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	// Generate external ID
	externalID := utils.NewOpaqueID()

	// TODO: Get tenantID and workspaceID from context (for now use defaults)
	tenantID := int64(1)
	workspaceID := int64(1)

	// Create forum in database
	forum, err := db.Storage.CreateForum(externalID, tenantID, workspaceID, u.ID, title, description, imageURL)
	if err != nil {
		log.Printf("forum creation error: %v", err)
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Error:   "Erro ao criar fórum. Tente novamente.",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_create.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	// Redirect to the new forum
	log.Printf("forum created: id=%d, external_id=%s, title=%s, user=%s", forum.ID, forum.ExternalID, forum.Title, u.Email)
	http.Redirect(w, r, fmt.Sprintf("/forum/%s", forum.ExternalID), http.StatusFound)
}

// editHandler handles GET /forum/{forumID}/edit - show edit form
func editHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	forumExtID := r.PathValue("forumID")

	// Fetch forum by external ID
	forum, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}

	// Check if user is the owner
	if forum.OwnerUserID != u.ID {
		http.Error(w, "you don't have permission to edit this forum", http.StatusForbidden)
		return
	}

	data := struct {
		Authed  bool
		User    db.User
		Config  *config.Config
		Forum   *db.Forum
		Error   string
		Message string
	}{
		Authed:  authed,
		User:    *u,
		Config:  config.Cfg,
		Forum:   forum,
		Error:   "",
		Message: "",
	}

	if err := templates.ExecuteTemplate(w, "forum_edit.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// editPostHandler handles POST /forum/{forumID}/edit and PATCH /post/{postID}
// based on the route pattern.
// Note: This function is overloaded to handle both forum edits and post edits
func editPostHandler(w http.ResponseWriter, r *http.Request) {
	// Check if this is a post edit (PATCH /post/{postID})
	if strings.Contains(r.RequestURI, "/post/") && r.Method == "PATCH" {
		editPostInThreadHandler(w, r)
		return
	}

	// Otherwise it's a forum edit
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	forumExtID := r.PathValue("forumID")

	// Fetch forum to verify ownership
	forum, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}

	// Check if user is the owner
	if forum.OwnerUserID != u.ID {
		http.Error(w, "you don't have permission to edit this forum", http.StatusForbidden)
		return
	}

	// Parse form data
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	imageURL := strings.TrimSpace(r.FormValue("imageURL"))

	// Validate title
	if title == "" {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forum,
			Error:   "Título é obrigatório",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_edit.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	if len(title) < 3 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forum,
			Error:   "Título deve ter pelo menos 3 caracteres",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_edit.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	if len(title) > 255 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forum,
			Error:   "Título não pode ultrapassar 255 caracteres",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_edit.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	// Update forum in database
	updatedForum, err := db.Storage.UpdateForum(forumExtID, title, description, imageURL)
	if err != nil {
		log.Printf("forum update error: %v", err)
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forum,
			Error:   "Erro ao atualizar fórum",
			Message: "",
		}
		if err := templates.ExecuteTemplate(w, "forum_edit.go.tmpl", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	// Redirect to the forum view
	log.Printf("forum updated: id=%d, external_id=%s, title=%s, user=%s", updatedForum.ID, updatedForum.ExternalID, updatedForum.Title, u.Email)
	http.Redirect(w, r, fmt.Sprintf("/forum/%s", forumExtID), http.StatusFound)
}

// threadViewHandler handles GET /forum/{forumID}/thread/{threadID}
func threadViewHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	forumExtID := r.PathValue("forumID")
	threadExtID := r.PathValue("threadID")

	// Check if this is a request to create a new thread
	if threadExtID == "create" {
		log.Printf("serving thread create form for forum %s, user %s", forumExtID, u.Email)

		// Fetch forum by external ID
		forumObj, err := db.Storage.GetForumByExternalID(forumExtID)
		if err != nil {
			http.Error(w, "forum not found", http.StatusNotFound)
			return
		}

		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  true,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forumObj,
			Thread:  nil,
			Error:   "",
			Message: "",
		}

		if err := templates.ExecuteTemplate(w, "thread_create.go.tmpl", data); err != nil {
			log.Printf("template %s execute error: %v", "thread_create.go.tmpl", err)
			http.Error(w, "template error", http.StatusInternalServerError)
		}
		return
	}

	// Normal thread view
	log.Printf("serving thread view for forum %s, thread %s, user %s", forumExtID, threadExtID, u.Email)

	// Fetch forum by external ID
	forumObj, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		log.Printf("ERROR: forum lookup error for forumID=%s: %v", forumExtID, err)
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}

	// Fetch thread by external ID
	threadObj, err := db.Storage.GetThreadByExternalID(threadExtID)
	if err != nil {
		log.Printf("ERROR: thread lookup error for threadID=%s: %v", threadExtID, err)
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}

	// Verify thread belongs to forum
	if threadObj.ForumID != forumObj.ID {
		log.Printf("ERROR: thread %d does not belong to forum %d", threadObj.ID, forumObj.ID)
		http.Error(w, "thread not found in forum", http.StatusNotFound)
		return
	}

	// Fetch posts for this thread
	posts, err := db.Storage.GetPostsByThreadID(threadObj.ID)
	if err != nil {
		log.Printf("ERROR: error getting posts for thread %d: %v", threadObj.ID, err)
		http.Error(w, "error loading posts", http.StatusInternalServerError)
		return
	}

	log.Printf("DEBUG: threadViewHandler - loaded %d posts for thread %s (thread_id=%d)", len(posts), threadExtID, threadObj.ID)

	// Build map of users for posts to include in template context
	userMap := make(map[int64]*db.User)
	for _, post := range posts {
		if _, exists := userMap[post.OwnerUserID]; !exists {
			user, err := db.Storage.GetUserByID(post.OwnerUserID)
			if err != nil {
				// Create minimal user entry on error
				log.Printf("warning: could not load user %d: %v", post.OwnerUserID, err)
				userMap[post.OwnerUserID] = &db.User{ID: post.OwnerUserID}
			} else {
				userMap[post.OwnerUserID] = user
			}
		}
	}

	data := struct {
		Authed  bool
		User    db.User
		Config  *config.Config
		Forum   *db.Forum
		Thread  *db.Thread
		Posts   []*db.Post
		UserMap map[int64]*db.User
	}{
		Authed:  true,
		User:    *u,
		Config:  config.Cfg,
		Forum:   forumObj,
		Thread:  threadObj,
		Posts:   posts,
		UserMap: userMap,
	}

	log.Printf("DEBUG: about to render thread_view.go.tmpl with Authed=%v, Forum.ID=%d, Thread.ID=%d, Posts=%d", data.Authed, data.Forum.ID, data.Thread.ID, len(data.Posts))

	if err := templates.ExecuteTemplate(w, "thread_view.go.tmpl", data); err != nil {
		log.Printf("ERROR: template %s execute error: %v", "thread_view.go.tmpl", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// createThreadHandler handles POST /forum/{forumID}/thread - create new thread
func createThreadHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	forumExtID := r.PathValue("forumID")

	log.Printf("creating thread in forum %s by user %s (user_id=%d)", forumExtID, u.Email, u.ID)

	// Parse form
	_ = r.ParseForm()

	// Validate
	title := strings.TrimSpace(r.FormValue("title"))
	content := strings.TrimSpace(r.FormValue("content"))
	imageURL := strings.TrimSpace(r.FormValue("imageURL"))

	log.Printf("DEBUG: received form data - title=%q (len=%d), content=%q (len=%d), imageURL=%q", title, len(title), content, len(content), imageURL)

	if len(title) < 3 || len(title) > 255 {
		log.Printf("DEBUG: title validation failed - len=%d, need 3-255", len(title))
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  true,
			User:    *u,
			Config:  config.Cfg,
			Forum:   nil,
			Thread:  nil,
			Error:   "Título deve ter entre 3 e 255 caracteres",
			Message: "",
		}
		templates.ExecuteTemplate(w, "thread_create.go.tmpl", data)
		return
	}

	// Fetch forum to get ID
	forumObj, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		log.Printf("ERROR: forum lookup failed for external_id=%s: %v", forumExtID, err)
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}
	log.Printf("DEBUG: forum loaded - id=%d, title=%s", forumObj.ID, forumObj.Title)

	// Create thread
	externalID := utils.NewOpaqueID()
	contentHTML := string(mdToHTML([]byte(content)))
	log.Printf("DEBUG: about to create thread: forumID=%d, ownerID=%d, title=%q, externalID=%s, imageURL=%q", forumObj.ID, u.ID, title, externalID, imageURL)
	threadObj, err := db.Storage.CreateThread(forumObj.ID, u.ID, title, externalID, imageURL)
	if err != nil {
		log.Printf("error creating thread: %v", err)
		http.Error(w, "error creating thread", http.StatusInternalServerError)
		return
	}
	log.Printf("DEBUG: thread created successfully: id=%d, external_id=%s", threadObj.ID, threadObj.ExternalID)

	// Create first post with the thread content
	postExternalID := utils.NewOpaqueID()
	post, err := db.Storage.CreatePost(threadObj.ID, u.ID, content, contentHTML, postExternalID, nil)
	if err != nil {
		log.Printf("error creating initial post: %v", err)
		http.Error(w, "error creating initial post", http.StatusInternalServerError)
		return
	}
	log.Printf("DEBUG: initial post created: id=%d, external_id=%s, thread_id=%d", post.ID, post.ExternalID, post.ThreadID)

	// Redirect to new thread
	log.Printf("thread created: external_id=%s, title=%s, user=%s", threadObj.ExternalID, threadObj.Title, u.Email)
	http.Redirect(w, r, fmt.Sprintf("/forum/%s/thread/%s", forumExtID, threadObj.ExternalID), http.StatusFound)
}

// threadEditHandler handles GET /forum/{forumID}/thread/{threadID}/edit - show edit form
func threadEditHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodGet},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	threadExtID := r.PathValue("threadID")

	// Fetch thread by external ID
	thread, err := db.Storage.GetThreadByExternalID(threadExtID)
	if err != nil {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}

	// Check if user is the owner
	if thread.OwnerUserID != u.ID {
		http.Error(w, "you don't have permission to edit this thread", http.StatusForbidden)
		return
	}

	// Fetch forum for context
	forumObj, err := db.Storage.GetForumByExternalID(r.PathValue("forumID"))
	if err != nil {
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}

	data := struct {
		Authed  bool
		User    db.User
		Config  *config.Config
		Forum   *db.Forum
		Thread  *db.Thread
		Error   string
		Message string
	}{
		Authed:  authed,
		User:    *u,
		Config:  config.Cfg,
		Forum:   forumObj,
		Thread:  thread,
		Error:   "",
		Message: "",
	}

	if err := templates.ExecuteTemplate(w, "thread_edit.go.tmpl", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// threadEditPostHandler handles POST /forum/{forumID}/thread/{threadID}/edit - save changes
func threadEditPostHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	threadExtID := r.PathValue("threadID")
	forumExtID := r.PathValue("forumID")

	// Fetch thread to verify ownership
	thread, err := db.Storage.GetThreadByExternalID(threadExtID)
	if err != nil {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}

	// Check if user is the owner
	if thread.OwnerUserID != u.ID {
		http.Error(w, "you don't have permission to edit this thread", http.StatusForbidden)
		return
	}

	// Fetch forum for context
	forumObj, err := db.Storage.GetForumByExternalID(forumExtID)
	if err != nil {
		http.Error(w, "forum not found", http.StatusNotFound)
		return
	}

	// Parse form data
	title := strings.TrimSpace(r.FormValue("title"))
	imageURL := strings.TrimSpace(r.FormValue("imageURL"))

	// Validate title
	if title == "" {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forumObj,
			Thread:  thread,
			Error:   "Título é obrigatório",
			Message: "",
		}
		templates.ExecuteTemplate(w, "thread_edit.go.tmpl", data)
		return
	}

	if len(title) < 3 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forumObj,
			Thread:  thread,
			Error:   "Título deve ter pelo menos 3 caracteres",
			Message: "",
		}
		templates.ExecuteTemplate(w, "thread_edit.go.tmpl", data)
		return
	}

	if len(title) > 255 {
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forumObj,
			Thread:  thread,
			Error:   "Título não pode ultrapassar 255 caracteres",
			Message: "",
		}
		templates.ExecuteTemplate(w, "thread_edit.go.tmpl", data)
		return
	}

	// Update thread in database
	updatedThread, err := db.Storage.UpdateThread(threadExtID, title, imageURL)
	if err != nil {
		log.Printf("thread update error: %v", err)
		data := struct {
			Authed  bool
			User    db.User
			Config  *config.Config
			Forum   *db.Forum
			Thread  *db.Thread
			Error   string
			Message string
		}{
			Authed:  authed,
			User:    *u,
			Config:  config.Cfg,
			Forum:   forumObj,
			Thread:  thread,
			Error:   "Erro ao atualizar tópico",
			Message: "",
		}
		templates.ExecuteTemplate(w, "thread_edit.go.tmpl", data)
		return
	}

	// Redirect to the thread view
	log.Printf("thread updated: id=%d, external_id=%s, title=%s, user=%s", updatedThread.ID, updatedThread.ExternalID, updatedThread.Title, u.Email)
	http.Redirect(w, r, fmt.Sprintf("/forum/%s/thread/%s", forumExtID, threadExtID), http.StatusFound)
}

// createPostInThreadHandler handles POST /forum/{forumID}/thread/{threadID}/post - create new post
func createPostInThreadHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPost},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return // auth.Prelude already handled redirect
	}

	threadExtID := r.PathValue("threadID")

	log.Printf("createPostInThreadHandler: threadExtID=%s, method=%s", threadExtID, r.Method)
	_ = r.ParseForm()
	log.Printf("createPostInThreadHandler: received content length=%d, raw content=%q", len(r.FormValue("content")), r.FormValue("content"))

	content := strings.TrimSpace(r.FormValue("content"))

	if content == "" {
		log.Printf("createPostInThreadHandler: content is empty")
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	// Fetch thread to get its ID
	threadObj, err := db.Storage.GetThreadByExternalID(threadExtID)
	if err != nil {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}

	log.Printf("creating post for thread %s by user %s", threadExtID, u.Email)

	// Create post
	postExternalID := utils.NewOpaqueID()
	contentHTML := string(mdToHTML([]byte(content)))

	post, err := db.Storage.CreatePost(threadObj.ID, u.ID, content, contentHTML, postExternalID, nil)
	if err != nil {
		log.Printf("error creating post: %v", err)
		http.Error(w, "error creating post", http.StatusInternalServerError)
		return
	}

	// Return the rendered post HTML partial for HTMX to append
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	data := struct {
		Post        *db.Post
		Author      *db.User
		CurrentUser db.User
	}{
		Post:        post,
		Author:      u,
		CurrentUser: *u,
	}

	if err := templates.ExecuteTemplate(w, "single_post", data); err != nil {
		log.Printf("template single_post execute error: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// editPostInThreadHandler handles PATCH /post/{postID} - edit existing post
func editPostInThreadHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodPatch},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		return
	}

	postExtID := r.PathValue("postID")

	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		// Fallback to ParseForm if multipart fails (in case content-type is form-urlencoded)
		if err := r.ParseForm(); err != nil {
			log.Printf("editPostInThreadHandler: parse error: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
	}

	content := strings.TrimSpace(r.FormValue("content"))

	if content == "" {
		log.Printf("editPostInThreadHandler: content is empty")
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	// Get post to verify ownership and get ID
	const sqlSelect = `SELECT id, external_id, owner_user_id, COALESCE(content, ''), COALESCE(content_html, ''), parent_post_id, created_at, updated_at FROM forum_posts WHERE external_id = ?`
	var post db.Post
	err = db.Storage.QueryRow(sqlSelect, postExtID).Scan(
		&post.ID,
		&post.ExternalID,
		&post.OwnerUserID,
		&post.Content,
		&post.ContentHTML,
		&post.ParentPostID,
		&post.CreatedAt,
		&post.UpdatedAt,
	)
	if err != nil {
		log.Printf("error getting post: %v", err)
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	// Check authorization: only post author can edit
	if post.OwnerUserID != u.ID {
		log.Printf("editPostInThreadHandler: user %d not authorized to edit post %d", u.ID, post.ID)
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	// Update post with new content and HTML
	contentHTML := string(mdToHTML([]byte(content)))

	updatedPost, err := db.Storage.UpdatePost(post.ID, content, contentHTML)
	if err != nil {
		log.Printf("error updating post: %v", err)
		http.Error(w, "error updating post", http.StatusInternalServerError)
		return
	}

	// Return the updated post HTML partial for HTMX to replace
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	data := struct {
		Post        *db.Post
		Author      *db.User
		CurrentUser db.User
	}{
		Post:        updatedPost,
		Author:      u,
		CurrentUser: *u,
	}

	if err := templates.ExecuteTemplate(w, "single_post", data); err != nil {
		log.Printf("template single_post execute error: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// deletePostHandler handles DELETE /post/{postID} - delete post
func deletePostHandler(w http.ResponseWriter, r *http.Request) {
	u, _, authed, err := auth.Prelude(w, r,
		[]string{http.MethodDelete},
		true,  // check auth
		false, // check ratelimit
		true,  // prevent cache
	)
	if err != nil {
		log.Printf("auth.Prelude error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !authed {
		log.Printf("deletePostHandler: user not authenticated")
		return
	}

	postExtID := r.PathValue("postID")

	// Get post to verify ownership
	const sqlSelect = `SELECT id, external_id, owner_user_id FROM forum_posts WHERE external_id = ?`
	var postID int64
	var ownerUserID int64
	err = db.Storage.QueryRow(sqlSelect, postExtID).Scan(&postID, &postExtID, &ownerUserID)
	if err != nil {
		log.Printf("error getting post: %v", err)
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	// Check authorization: only post author can delete
	if ownerUserID != u.ID {
		log.Printf("deletePostHandler: user %d not authorized to delete post %d", u.ID, postID)
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	// Delete the post
	err = db.Storage.DeletePost(postID)
	if err != nil {
		log.Printf("error deleting post: %v", err)
		http.Error(w, "error deleting post", http.StatusInternalServerError)
		return
	}

	// Return 204 No Content for successful delete
	w.WriteHeader(http.StatusNoContent)
}
