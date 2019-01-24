package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type article struct {
	CreationTime time.Time
	UpdateTime   time.Time
	Title        string
	Body         template.HTML
	ID           int
}

type templateVars struct {
	GenerateArticlesHTML      bool
	GenerateArticleHTML       bool
	GenerateAboutHTML         bool
	GenerateContactHTML       bool
	Articles                  []article
	ArticleIndex              int
}

func main() {
	log.SetFlags(log.Lshortfile)

	inProduction := flag.Bool("production", false, "in production")
	flag.Parse()

	articles := make([]article, 0, 256)
	// {
	// 	var article_rows *sql.Rows
	// 	article_rows, err = db.Query("SELECT * FROM articles ORDER By creation_time DESC")
	// 	if err == nil {
	// 		defer article_rows.Close()
	// 		for article_rows.Next() {
	// 			var article article
	// 			article_rows.Scan(&article.CreationTime, &article.UpdateTime, &article.Title, &article.Body, &article.ID)
	// 			var comment_rows *sql.Rows
	// 			comment_rows, err = db.Query("SELECT * FROM comments WHERE article_id = $1 ORDER by creation_time DESC", article.ID)
	// 			if err == nil {
	// 				defer comment_rows.Close()
	// 				for comment_rows.Next() {
	// 					var comment comment
	// 					comment_rows.Scan(&comment.CommenterName, &comment.CommenterEmail, &comment.Text, &comment.CreationTime, &comment.ArticleID, &comment.CommenterIP)
	// 					article.Comments = append(article.Comments, comment)
	// 				}
	// 				if comment_rows.Err() != nil {
	// 					log.Fatal(comment_rows.Err())
	// 				}
	// 			} else {
	// 				log.Print(err)
	// 			}
	// 			articles = append(articles, article)
	// 		}
	// 		if article_rows.Err() != nil {
	// 			log.Fatal(article_rows.Err())
	// 		}
	// 	} else {
	// 		log.Print(err)
	// 	}
	// }

	htmlTemplate, err := template.New("template.html").ParseFiles("template.html")
	if err != nil {
		log.Fatal(err)
	}

	aboutHTML := new(bytes.Buffer)
	{
		var tvars templateVars
		tvars.GenerateAboutHTML = true
		err := htmlTemplate.Execute(aboutHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	contactHTML := new(bytes.Buffer)
	{
		var tvars templateVars
		tvars.GenerateContactHTML = true
		err := htmlTemplate.Execute(contactHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	articlesHTML := new(bytes.Buffer)
	{
		var tvars templateVars
		tvars.GenerateArticlesHTML = true
		tvars.Articles = articles
		err := htmlTemplate.Execute(articlesHTML, tvars)
		if err != nil {
			log.Fatal(err)
		}
	}
	articleHTMLs := make([]*bytes.Buffer, len(articles), len(articles))
	{
		for i, _ := range articles {
			articleHTMLs[i] = new(bytes.Buffer)

			var tvars templateVars
			tvars.GenerateArticleHTML = true
			tvars.Articles = articles
			tvars.ArticleIndex = i
			err := htmlTemplate.Execute(articleHTMLs[i], tvars)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	redirectHTTPSMux := new(http.ServeMux)
	mux := new(http.ServeMux)

	redirectHTTPSMux.HandleFunc("/", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, "https://"+request.Host+request.URL.String(), http.StatusMovedPermanently)
	}))
	mux.HandleFunc("/about", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, aboutHTML)
	}))
	mux.HandleFunc("/contact", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, contactHTML)
	}))
	mux.HandleFunc("/articles", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, articlesHTML)
	}))
	for i, article := range articles {
		mux.HandleFunc("/articles/"+strconv.Itoa(article.ID), http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			fmt.Fprint(response, articleHTMLs[i])
		}))
	}
	mux.Handle("/assets/", http.StripPrefix("/assets", http.FileServer(http.Dir("./assets"))))
	mux.Handle("/favicon.ico", http.NotFoundHandler())
	mux.Handle("/", http.RedirectHandler("/articles", http.StatusFound))

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
