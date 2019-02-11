package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type article struct {
	ID         int
	Title      string
	CreateTime time.Time
	UpdateTime time.Time
	HTML       template.HTML
	Javascript template.HTML
}

type templateVars struct {
	GenerateType string
	Articles     []article
	ArticleIndex int
}

func main() {
	log.SetFlags(log.Lshortfile)

	inProduction := flag.Bool("production", false, "in production")
	flag.Parse()

	articles := make([]article, 0, 256)
	{
		files, err := ioutil.ReadDir(".")
		if err != nil {
			log.Fatal(err)
		}
		ID := 1
		for _, file := range files {
			fileName := file.Name()
			if strings.HasPrefix(fileName, "article_") {
				fileBytes, err := ioutil.ReadFile(fileName)
				if err != nil {
					log.Fatal(err)
				}
				str := string(fileBytes[:])
				str = strings.Replace(str, "\r\n", "\n", -1)
				strs := strings.SplitN(str, "---separator---", -1)
				if len(strs) != 5 {
					log.Fatal("")
				}
				for i, _ := range strs {
					strs[i] = strings.TrimSpace(strs[i])
				}
				timeLayout := "Jan 2, 2006"
				article := article{}
				article.ID = ID
				ID += 1
				article.Title = strs[0]
				article.CreateTime, err = time.Parse(timeLayout, strs[1])
				if err != nil {
					log.Fatal(err)
				}
				article.UpdateTime, err = time.Parse(timeLayout, strs[2])
				if err != nil {
					log.Fatal(err)
				}
				article.HTML = template.HTML(strs[3])
				article.Javascript = template.HTML(strs[4])
				articles = append(articles, article)
			}
		}
		sort.Slice(articles, func(i, j int) bool { return articles[i].CreateTime.After(articles[j].CreateTime) })
	}

	htmlTemplate, err := template.New("template.html").ParseFiles("script.html", "template.html")
	if err != nil {
		log.Fatal(err)
	}
	var tmplVars templateVars
	tmplVars.Articles = articles

	aboutHTML := new(bytes.Buffer)
	{
		tmplVars.GenerateType = "GenerateAboutHTML"
		err := htmlTemplate.Execute(aboutHTML, tmplVars)
		if err != nil {
			log.Fatal(err)
		}
	}
	contactHTML := new(bytes.Buffer)
	{
		tmplVars.GenerateType = "GenerateContactHTML"
		err := htmlTemplate.Execute(contactHTML, tmplVars)
		if err != nil {
			log.Fatal(err)
		}
	}
	articleHTMLs := make([]*bytes.Buffer, len(articles), len(articles))
	{
		for i, _ := range articles {
			articleHTMLs[i] = new(bytes.Buffer)
			tmplVars.GenerateType = "GenerateArticleHTML"
			tmplVars.ArticleIndex = i
			err := htmlTemplate.Execute(articleHTMLs[i], tmplVars)
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	redirectHTTPSMux := new(http.ServeMux)
	mux := new(http.ServeMux)

	redirectHTTPSMux.HandleFunc("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.URL.String(), http.StatusMovedPermanently)
	}))
	mux.HandleFunc("/favicon.png", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "favicon.png")
	}))
	mux.HandleFunc("/about", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, aboutHTML)
	}))
	mux.HandleFunc("/contact", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, contactHTML)
	}))
	for i, article := range articles {
		articleHTML := articleHTMLs[i]
		mux.HandleFunc("/articles/"+strconv.Itoa(article.ID), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, articleHTML)
		}))
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, articleHTMLs[0])
	}))

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
			var fullchain = "/etc/letsencrypt/live/mynameisyc.com/fullchain.pem"
			var privKey = "/etc/letsencrypt/live/mynameisyc.com/privkey.pem"
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
