package main

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type comment struct {
	CommenterName  string
	CommenterEmail string
	Text           string
	CreationTime   time.Time
	ArticleID      int
	CommenterIP    string
}

type article struct {
	CreationTime time.Time
	UpdateTime   time.Time
	Title        string
	Body         template.HTML
	ID           int
	Comments     []comment
}

type templateVars struct {
	GenerateBlogHTML          bool
	GenerateArticleHTML       bool
	GenerateArticleUpdateHTML bool
	GenerateAboutHTML         bool
	GenerateContactHTML       bool
	Articles                  []article
	ArticleIndex              int
}

func EmailHash(email string) string {
	hasher := md5.New()
	hasher.Write([]byte(email))
	return hex.EncodeToString(hasher.Sum(nil))
}

type HTML struct {
	html *bytes.Buffer
	lock sync.RWMutex
}

const (
	newArticleFlag    = iota
	updateArticleFlag = iota
	newCommentFlag    = iota
)

type articleUpdateResponse struct {
	success bool
	msg     string
}

type articleUpdate struct {
	kind            int
	article         article
	comment         comment
	responseChannel chan articleUpdateResponse
}

func main() {
	log.SetFlags(log.Lshortfile)

	dbPassword := flag.String("dbpass", "", "postgresql password")
	inProduction := flag.Bool("production", false, "in production")
	flag.Parse()

	dbConnStr := "user=postgres dbname=ycssite sslmode=disable password=" + *dbPassword
	db, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Query("SELECT version()")
	if err != nil {
		log.Fatal(err)
	}

	var articles []article
	{
		articles = make([]article, 0, 256)
		var article_rows *sql.Rows
		article_rows, err = db.Query("SELECT * FROM articles ORDER By creation_time DESC")
		if err == nil {
			defer article_rows.Close()
			for article_rows.Next() {
				var article article
				article_rows.Scan(&article.CreationTime, &article.UpdateTime, &article.Title, &article.Body, &article.ID)
				var comment_rows *sql.Rows
				comment_rows, err = db.Query("SELECT * FROM comments WHERE article_id = $1 ORDER by creation_time DESC", article.ID)
				if err == nil {
					defer comment_rows.Close()
					for comment_rows.Next() {
						var comment comment
						comment_rows.Scan(&comment.CommenterName, &comment.CommenterEmail, &comment.Text, &comment.CreationTime, &comment.ArticleID, &comment.CommenterIP)
						article.Comments = append(article.Comments, comment)
					}
					if comment_rows.Err() != nil {
						log.Fatal(comment_rows.Err())
					}
				} else {
					log.Print(err)
				}
				articles = append(articles, article)
			}
			if article_rows.Err() != nil {
				log.Fatal(article_rows.Err())
			}
		} else {
			log.Print(err)
		}
	}

	htmlTemplate := template.New("template.html").Funcs(template.FuncMap{"EmailHash": EmailHash})
	htmlTemplate, err = htmlTemplate.ParseFiles("template.html")
	if err != nil {
		log.Fatal(err)
	}

	aboutHTML := HTML{new(bytes.Buffer), sync.RWMutex{}}
	{
		var tvars templateVars
		tvars.GenerateAboutHTML = true
		err = htmlTemplate.Execute(aboutHTML.html, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	contactHTML := HTML{new(bytes.Buffer), sync.RWMutex{}}
	{
		var tvars templateVars
		tvars.GenerateContactHTML = true
		err = htmlTemplate.Execute(contactHTML.html, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	articleUpdateHTML := HTML{new(bytes.Buffer), sync.RWMutex{}}
	{
		var tvars templateVars
		tvars.GenerateArticleUpdateHTML = true
		err = htmlTemplate.Execute(articleUpdateHTML.html, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	blogHTML := HTML{new(bytes.Buffer), sync.RWMutex{}}
	{
		var tvars templateVars
		tvars.GenerateBlogHTML = true
		tvars.Articles = articles
		err = htmlTemplate.Execute(blogHTML.html, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	articleHTMLs := make([]*HTML, len(articles), len(articles))
	{
		for i, _ := range articles {
			articleHTMLs[i] = new(HTML)
			articleHTMLs[i].html = new(bytes.Buffer)
			articleHTMLs[i].lock = sync.RWMutex{}

			var tvars templateVars
			tvars.GenerateArticleHTML = true
			tvars.Articles = articles
			tvars.ArticleIndex = i
			err = htmlTemplate.Execute(articleHTMLs[i].html, tvars)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	redirectHTTPSMux := new(http.ServeMux)
	mux := new(http.ServeMux)
	articleUpdateChannel := make(chan articleUpdate)

	redirectHTTPSMux.HandleFunc("/", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://"+request.Host+request.URL.String(), http.StatusMovedPermanently)
	}))

	mux.HandleFunc("/blog", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		html := new(bytes.Buffer)
		blogHTML.lock.RLock()
		html.Write(blogHTML.html.Bytes())
		blogHTML.lock.RUnlock()
		fmt.Fprint(response, html)
	}))

	for i, article := range articles {
		articleHTML := articleHTMLs[i]
		mux.HandleFunc("/blog/"+strconv.Itoa(article.ID), http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			html := new(bytes.Buffer)
			articleHTML.lock.RLock()
			html.Write(articleHTML.html.Bytes())
			articleHTML.lock.RUnlock()
			fmt.Fprint(response, html)
		}))
	}

	mux.HandleFunc("/blog/article", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == "GET" {
			fmt.Fprint(response, articleUpdateHTML.html)
		} else if request.Method == "POST" {
			err := request.ParseForm()
			if err == nil {
				password := request.PostForm.Get("password")
				if password == *dbPassword {
					idField := request.PostForm.Get("article-id")
					id, err := strconv.Atoi(idField)
					if err != nil {
						http.Error(response, "Error parsing article ID", http.StatusInternalServerError)
					} else {
						var update articleUpdate
						if id < 0 {
							update.kind = newArticleFlag
						} else {
							update.kind = updateArticleFlag
						}
						title := request.PostForm.Get("article-title")
						body := request.PostForm.Get("article-body")
						update.article = article{time.Now(), time.Now(), title, template.HTML(body), id, []comment{}}
						update.responseChannel = make(chan articleUpdateResponse)
						articleUpdateChannel <- update
						updateResponse := <-update.responseChannel
						if updateResponse.success {
							if id < 0 {
								fmt.Fprintf(response, "New article submitted")
							} else {
								fmt.Fprintf(response, "Article %d updated", id)
							}
						} else {
							http.Error(response, updateResponse.msg, http.StatusInternalServerError)
						}
					}
				} else {
					http.Error(response, "Wrong password", http.StatusInternalServerError)
				}
			} else {
				http.Error(response, "Error parsing form", http.StatusInternalServerError)
			}
		} else {
			http.NotFound(response, request)
		}
	}))
	mux.HandleFunc("/blog/comment", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == "POST" {
			err := request.ParseForm()
			if err == nil {
				articleID := request.PostForm.Get("article-id")
				if articleID != "" {
					id, err := strconv.Atoi(articleID)
					if err == nil {
						name := request.PostForm.Get("name")
						email := request.PostForm.Get("email")
						text := request.PostForm.Get("text")
						if name != "" && text != "" {
							var update articleUpdate
							update.kind = newCommentFlag
							update.comment = comment{name, email, text, time.Now(), id, request.RemoteAddr}
							update.responseChannel = make(chan articleUpdateResponse)
							articleUpdateChannel <- update
							updateResponse := <-update.responseChannel
							if updateResponse.success {
								http.Redirect(response, request, "/blog/"+articleID, http.StatusFound)
							} else {
								http.Error(response, updateResponse.msg, http.StatusInternalServerError)
							}
						} else {
							http.Error(response, "Missing commenter name or text", http.StatusInternalServerError)
						}
					} else {
						http.Error(response, "Error parsing article id", http.StatusInternalServerError)
					}
				} else {
					http.Error(response, "Missing article id", http.StatusInternalServerError)
				}
			} else {
				http.Error(response, "Error parsing form", http.StatusInternalServerError)
			}
		} else {
			http.NotFound(response, request)
		}
	}))
	mux.HandleFunc("/about", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, aboutHTML.html)
	}))
	mux.HandleFunc("/contact", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, contactHTML.html)
	}))
	mux.Handle("/assets/", http.StripPrefix("/assets", http.FileServer(http.Dir("./assets"))))
	mux.Handle("/favicon.ico", http.NotFoundHandler())
	mux.Handle("/", http.RedirectHandler("/blog", http.StatusFound))

	go func() {
		updateBlogHTML := func() {
			newBlogHTML := new(bytes.Buffer)
			var tvars templateVars
			tvars.GenerateBlogHTML = true
			tvars.Articles = articles
			htmlTemplate.Execute(newBlogHTML, tvars)

			blogHTML.lock.Lock()
			blogHTML.html = newBlogHTML
			blogHTML.lock.Unlock()
		}
		updateArticleHTML := func(i int) {
			newArticleHTML := new(bytes.Buffer)
			var tvars templateVars
			tvars.GenerateArticleHTML = true
			tvars.Articles = articles
			tvars.ArticleIndex = i
			htmlTemplate.Execute(newArticleHTML, tvars)

			articleHTMLs[i].lock.Lock()
			articleHTMLs[i].html = newArticleHTML
			articleHTMLs[i].lock.Unlock()
		}
		for update := range articleUpdateChannel {
			if update.kind == newArticleFlag {
				_, err := db.Exec("INSERT INTO articles (title, body) VALUES ($1, $2)", update.article.Title, update.article.Body)
				if err != nil {
					update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Error inserting article into database: %s", err)}
				} else {
					var id int
					err := db.QueryRow("SELECT id FROM articles WHERE title=$1", update.article.Title).Scan(&id)
					if err != nil {
						update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Article inserted into database but can't be queried, this should never happen: %s", err)}
					} else {
						update.article.ID = id
						articles = append([]article{update.article}, articles...)
						newArticleHTML := new(HTML)
						newArticleHTML.html = new(bytes.Buffer)
						newArticleHTML.lock = sync.RWMutex{}
						articleHTMLs = append([]*HTML{newArticleHTML}, articleHTMLs...)
						updateBlogHTML()
						updateArticleHTML(0)
						mux.HandleFunc("/blog/"+strconv.Itoa(id), http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
							html := new(bytes.Buffer)
							newArticleHTML.lock.RLock()
							html.Write(newArticleHTML.html.Bytes())
							newArticleHTML.lock.RUnlock()
							fmt.Fprint(response, html)
						}))
						update.responseChannel <- articleUpdateResponse{true, ""}
					}
				}
			} else if update.kind == updateArticleFlag {
				var result sql.Result
				if update.article.Title != "" && update.article.Body != "" {
					result, err = db.Exec(
						"UPDATE articles SET update_time=$1, title=$2, body=$3 WHERE id=$4",
						update.article.UpdateTime, update.article.Title, update.article.Body, update.article.ID)
				} else if update.article.Title != "" {
					result, err = db.Exec(
						"UPDATE articles SET update_time=$1, title=$2 WHERE id=$3",
						update.article.UpdateTime, update.article.Title, update.article.ID)
				} else if update.article.Body != "" {
					result, err = db.Exec(
						"UPDATE articles SET update_time=$1, body=$2 WHERE id=$3",
						update.article.UpdateTime, update.article.Body, update.article.ID)
				} else {
					update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Article Update not performed since both title and body text are empty")}
					continue
				}
				if err != nil {
					update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Error updating article with ID %d in database: %s", update.article.ID, err)}
				} else {
					n, err := result.RowsAffected()
					if err != nil {
						update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Error updating article with ID %d in database: %s", update.article.ID, err)}
					} else if n < 1 {
						update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Article ID %d not found in database", update.article.ID)}
					} else if n > 1 {
						update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Mutliple Articles with same ID %d found and updated in database, this should never happen", update.article.ID)}
					} else {
						updated := false
						for i, _ := range articles {
							if articles[i].ID == update.article.ID {
								articles[i].UpdateTime = update.article.UpdateTime
								if update.article.Title != "" {
									articles[i].Title = update.article.Title
								}
								if update.article.Body != "" {
									articles[i].Body = update.article.Body
								}
								updateBlogHTML()
								updateArticleHTML(i)
								updated = true
								break
							}
						}
						if updated {
							update.responseChannel <- articleUpdateResponse{true, ""}
						} else {
							update.responseChannel <- articleUpdateResponse{false, "Article updated in database but ID not found in memory"}
						}
					}
				}
			} else if update.kind == newCommentFlag {
				for i, _ := range articles {
					article := &articles[i]
					if article.ID == update.comment.ArticleID {
						if len(article.Comments) >= 512 {
							update.responseChannel <- articleUpdateResponse{false, "Article comments maxed out"}
						} else {
							_, err := db.Exec(
								"INSERT INTO comments VALUES ($1, $2, $3, $4, $5, $6)",
								update.comment.CommenterName,
								update.comment.CommenterEmail,
								update.comment.Text,
								update.comment.CreationTime,
								update.comment.ArticleID,
								update.comment.CommenterIP)
							if err != nil {
								update.responseChannel <- articleUpdateResponse{false, fmt.Sprintf("Error inserting comment into database: %s", err)}
							} else {
								article.Comments = append([]comment{update.comment}, article.Comments...)
								updateArticleHTML(i)
								update.responseChannel <- articleUpdateResponse{true, ""}
							}
						}
						break
					}
				}
			}
		}
	}()

	makeServer := func(mux *http.ServeMux) http.Server {
		return http.Server{
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  120 * time.Second,
			Handler:      mux,
		}
	}
	if *inProduction {
		httpsServer := makeServer(mux)
		httpsServer.Addr = ":443"
		go func() {
			var fullchain = "/etc/letsencrypt/live/ycssite.com/fullchain.pem"
			var privKey = "/etc/letsencrypt/live/ycssite.com/privkey.pem"
			err := httpsServer.ListenAndServeTLS(fullchain, privKey)
			if err != nil {
				log.Fatal(err)
			}
		}()
		httpServer := makeServer(redirectHTTPSMux)
		httpServer.Addr = ":80"
		fmt.Print("\nstarted serving requests...\n")
		err := httpServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	} else {
		httpServer := makeServer(mux)
		httpServer.Addr = ":7000"
		fmt.Print("\nstarted serving requests...\n")
		err := httpServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	}
}
