package main

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
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

type articleComment struct {
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
	HTML         template.HTML
	ID           int
	Comments     []articleComment
}

type articleNewComment struct {
	Comment         articleComment
	ResponseChannel chan bool
}

type templateVars struct {
	GenerateBlogHTML    bool
	GenerateArticleHTML bool
	GenerateAboutHTML   bool
	GenerateContactHTML bool
	Articles            []article
	ArticleIndex        int
}

func ReverseComments(comments []articleComment) []articleComment {
	reverse_comments := make([]articleComment, len(comments))
	for i := 0; i < len(comments); i++ {
		reverse_comments[i] = comments[len(comments)-i-1]
	}
	return reverse_comments
}

func EmailHash(email string) string {
	hasher := md5.New()
	hasher.Write([]byte(email))
	return hex.EncodeToString(hasher.Sum(nil))
}

func main() {
	var err error

	log.SetFlags(log.Lshortfile)

	dbPassword := flag.String("dbpass", "", "postgresql password")
	inProduction := flag.Bool("production", false, "in production")
	importArticleFile := flag.String("import-article", "", "import article from file")
	flag.Parse()

	var db *sql.DB
	var dbConnStr string
	dbConnStr = "user=postgres dbname=ycssite sslmode=disable "
	if *dbPassword != "" {
		dbConnStr = dbConnStr + "password=" + *dbPassword
	}
	db, err = sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Query("SELECT version()")
	if err != nil {
		log.Fatal(err)
	}

	if *importArticleFile != "" {
		fileData, err := ioutil.ReadFile(*importArticleFile)
		if err != nil {
			log.Fatal(err)
		}
		fileName := filepath.Base(*importArticleFile)
		fileName = fileName[0 : len(fileName)-len(filepath.Ext(fileName))]
		_, err = db.Exec("INSERT INTO articles (title, body) VALUES ($1, $2)", fileName, string(fileData))
		if err != nil {
			if strings.Contains(err.Error(), "unique_title") {
				fmt.Print("Article with same title already exist, update it? Y/N ")
				for {
					var yes_no string
					fmt.Scan(&yes_no)
					strings.ToLower(yes_no)
					if yes_no == "y" || yes_no == "yes" {
						_, err = db.Exec("UPDATE articles SET body=$1 where title=$2", string(fileData), fileName)
						if err != nil {
							fmt.Println("article update error:")
							log.Fatal(err)
						} else {
							fmt.Println("article updated")
						}
						break
					} else if yes_no == "n" || yes_no == "no" {
						fmt.Println("update not performed")
						break
					} else {
						fmt.Print("please enter Y/N ")
					}
				}
			} else {
				log.Fatal(err)
			}
		} else {
			fmt.Println("article import successful")
		}
		return
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
				var html string
				article_rows.Scan(&article.CreationTime, &article.UpdateTime, &article.Title, &html, &article.ID)
				article.HTML = template.HTML(html)
				var comment_rows *sql.Rows
				comment_rows, err = db.Query("SELECT * FROM comments WHERE article_id = $1", article.ID)
				if err == nil {
					defer comment_rows.Close()
					for comment_rows.Next() {
						var comment articleComment
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

	htmlTemplate := template.New("template.html").Funcs(template.FuncMap{"ReverseComments": ReverseComments, "EmailHash": EmailHash})
	htmlTemplate, err = htmlTemplate.ParseFiles("template.html")
	if err != nil {
		log.Fatal(err)
	}

	var aboutHTML *bytes.Buffer
	{
		var tvars templateVars
		tvars.GenerateAboutHTML = true
		aboutHTML = new(bytes.Buffer)
		err = htmlTemplate.Execute(aboutHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	var contactHTML *bytes.Buffer
	{
		var tvars templateVars
		tvars.GenerateContactHTML = true
		contactHTML = new(bytes.Buffer)
		err = htmlTemplate.Execute(contactHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}

	var blogHTML *bytes.Buffer
	var blogHTMLLock sync.RWMutex
	{
		var tvars templateVars
		tvars.GenerateBlogHTML = true
		tvars.Articles = articles
		blogHTML = new(bytes.Buffer)
		err = htmlTemplate.Execute(blogHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}

	var articleHTMLs []*bytes.Buffer
	var articleHTMLLocks []sync.RWMutex
	{
		articleHTMLs = make([]*bytes.Buffer, len(articles), len(articles))
		articleHTMLLocks = make([]sync.RWMutex, len(articles), len(articles))
		for i, _ := range articles {
			var tvars templateVars
			tvars.GenerateArticleHTML = true
			tvars.Articles = articles
			tvars.ArticleIndex = i
			articleHTMLs[i] = new(bytes.Buffer)
			err = htmlTemplate.Execute(articleHTMLs[i], tvars)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	articleNewCommentChannel := make(chan articleNewComment)
	go func() {
		for newComment := range articleNewCommentChannel {
			for i, _ := range articles {
				article := &articles[i]
				if article.ID == newComment.Comment.ArticleID {
					if len(article.Comments) >= 512 {
						newComment.ResponseChannel <- false
						break
					} else {
						_, err = db.Exec(
							"INSERT INTO comments VALUES ($1, $2, $3, $4, $5, $6)",
							newComment.Comment.CommenterName,
							newComment.Comment.CommenterEmail,
							newComment.Comment.Text,
							newComment.Comment.CreationTime,
							newComment.Comment.ArticleID,
							newComment.Comment.CommenterIP)
						if err != nil {
							newComment.ResponseChannel <- false
							break
						} else {
							article.Comments = append(article.Comments, newComment.Comment)

							newBlogHTML := new(bytes.Buffer)
							var tvars templateVars
							tvars.GenerateBlogHTML = true
							tvars.Articles = articles
							htmlTemplate.Execute(newBlogHTML, tvars)

							newArticleHTML := new(bytes.Buffer)
							tvars.GenerateBlogHTML = false
							tvars.GenerateArticleHTML = true
							tvars.ArticleIndex = i
							htmlTemplate.Execute(newArticleHTML, tvars)
							fmt.Print(tvars.Articles[i].Comments)

							blogHTMLLock.Lock()
							blogHTML = newBlogHTML
							blogHTMLLock.Unlock()
							articleHTMLLocks[i].Lock()
							articleHTMLs[i] = newArticleHTML
							articleHTMLLocks[i].Unlock()
							newComment.ResponseChannel <- true
						}
					}
				}
			}
		}
	}()

	makeServer := func(redirectHTTPS bool) *http.Server {
		mux := &http.ServeMux{}
		if redirectHTTPS {
			mux.HandleFunc("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
			}))
		} else {
			mux.HandleFunc("/blog", http.HandlerFunc(func(httpResponseWriter http.ResponseWriter, httpRequest *http.Request) {
				response := new(bytes.Buffer)
				blogHTMLLock.RLock()
				response.Write(blogHTML.Bytes())
				blogHTMLLock.RUnlock()
				fmt.Fprint(httpResponseWriter, response)
			}))
			for i, article := range articles {
				index := i
				mux.HandleFunc("/blog/"+strconv.Itoa(article.ID), http.HandlerFunc(func(httpResponseWriter http.ResponseWriter, httpRequest *http.Request) {
					response := new(bytes.Buffer)
					articleHTMLLocks[index].RLock()
					response.Write(articleHTMLs[index].Bytes())
					articleHTMLLocks[index].RUnlock()
					fmt.Fprint(httpResponseWriter, response)
				}))
			}
			mux.HandleFunc("/blog/comment", http.HandlerFunc(func(httpResponseWriter http.ResponseWriter, httpRequest *http.Request) {
				if httpRequest.Method == "POST" {
					err := httpRequest.ParseForm()
					if err == nil {
						articleID := httpRequest.PostForm.Get("article-id")
						if articleID != "" {
							id, err := strconv.Atoi(articleID)
							if err == nil {
								name := httpRequest.PostForm.Get("name")
								email := httpRequest.PostForm.Get("email")
								text := httpRequest.PostForm.Get("text")
								if name != "" && text != "" {
									var newComment articleNewComment
									newComment.Comment = articleComment{name, email, text, time.Now(), id, httpRequest.RemoteAddr}
									newComment.ResponseChannel = make(chan bool)
									articleNewCommentChannel <- newComment
									success := <-newComment.ResponseChannel
									if success {
										http.Redirect(httpResponseWriter, httpRequest, "/blog/"+articleID, http.StatusFound)
										return
									}
								}
							}
						}
					}
				}
				http.Error(httpResponseWriter, "An error occured posting your comment...", http.StatusInternalServerError)
			}))
			mux.HandleFunc("/about", http.HandlerFunc(func(httpResponseWriter http.ResponseWriter, httpRequest *http.Request) {
				fmt.Fprint(httpResponseWriter, aboutHTML)
			}))
			mux.HandleFunc("/contact", http.HandlerFunc(func(httpResponseWriter http.ResponseWriter, httpRequest *http.Request) {
				fmt.Fprint(httpResponseWriter, contactHTML)
			}))
			mux.Handle("/assets/", http.StripPrefix("/assets", http.FileServer(http.Dir("./assets"))))
			mux.Handle("/favicon.ico", http.NotFoundHandler())
			mux.Handle("/", http.RedirectHandler("/blog", http.StatusFound))
		}
		return &http.Server{
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  120 * time.Second,
			Handler:      mux,
		}
	}

	if *inProduction {
		httpsServer := makeServer(false)
		httpsServer.Addr = ":443"
		go func() {
			var fullchain = "/etc/letsencrypt/live/ycssite.com/fullchain.pem"
			var privKey = "/etc/letsencrypt/live/ycssite.com/privkey.pem"
			err := httpsServer.ListenAndServeTLS(fullchain, privKey)
			if err != nil {
				log.Fatal(err)
			}
		}()
		httpServer := makeServer(true)
		httpServer.Addr = ":80"
		fmt.Print("\nstarted serving requests...\n")
		err = httpServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	} else {
		httpServer := makeServer(false)
		httpServer.Addr = ":8000"
		fmt.Print("\nstarted serving requests...\n")
		err = httpServer.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	}
}
