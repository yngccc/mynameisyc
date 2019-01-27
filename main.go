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

	redirectHTTPSMux.HandleFunc("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://" + r.Host + r.URL.String(), http.StatusMovedPermanently)
	}))
	mux.HandleFunc("/about", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, aboutHTML)
	}))
	mux.HandleFunc("/contact", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, contactHTML)
	}))
	mux.HandleFunc("/articles", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, articlesHTML)
	}))
	for i, article := range articles {
		mux.HandleFunc("/articles/"+strconv.Itoa(article.ID), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, articleHTMLs[i])
		}))
	}
	mux.HandleFunc("/favicon.png", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "favicon.png")
	}))
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
