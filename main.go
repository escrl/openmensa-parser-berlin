package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	urlBase   = "https://www.stw.berlin/xhr/"
	urlMeta   = "speiseplan-und-standortdaten.html"
	urlMeal   = "speiseplan-wochentag.html"
	defaultID = "321"

	urlFeedBase = "https://raw.githubusercontent.com/escrl/openmensa-feed-berlin/master/"

	indexFile    = "berlin/index.json"
	metadataBase = "berlin/"
	feedBase     = "berlin/"

	httpMaxRetries = 10
	httpSleepStep  = time.Second
)

func getHttpDoc(url string, data url.Values) (doc *goquery.Document) {
	for i := 1; i <= httpMaxRetries; i++ {
		resp, err := http.PostForm(url, data)
		if err != nil {
			panic(err)
		}
		if resp.StatusCode == http.StatusOK {
			doc, err := goquery.NewDocumentFromResponse(resp)
			if err != nil {
				panic(err)
			}
			return doc
		}
		if i < httpMaxRetries {
			// increase sleep duration every step
			sleepTime := time.Duration(i) * httpSleepStep
			log.Printf("sleep for %s before doing POST fetch at %s with %s", sleepTime, url, data)
			time.Sleep(sleepTime)
		} else {
			log.Printf("aborting after %s retries for POST fetch at %s with %ss", httpMaxRetries, url, data)
		}
	}
	return
}

func getIDs() []string {
	//	return []string{defaultID} //TODO debug
	doc := getHttpDoc(urlBase+urlMeta, url.Values{"resources_id": {defaultID}})

	return doc.Find("select#listboxEinrichtungen.listboxStandorte option[value]").Map(func(i int, s *goquery.Selection) string {
		id, _ := s.Attr("value")
		return id
	})
}

func genIndex(w io.Writer) error {
	index := map[string]string{}

	for _, id := range getIDs() {
		index[id] = urlFeedBase + id + ".xml"
	}

	enc := json.NewEncoder(w)
	err := enc.Encode(index)
	return err
}

func getMetadata(id string) *Canteen {
	doc := getHttpDoc(urlBase+urlMeta, url.Values{"resources_id": {id}})

	name := doc.Find("select#listboxEinrichtungen.listboxStandorte option[selected]").Text()

	address := doc.Find("i.glyphicon.glyphicon-map-marker").Parent().Next().Text()
	re := regexp.MustCompile(`\(.*\)`)
	address = re.ReplaceAllString(address, "")
	re = regexp.MustCompile(`\b.*\b`)
	address = strings.Join(re.FindAllString(address, 2), ", ")

	phone := strings.TrimSpace(doc.Find("i.glyphicon.glyphicon-earphone").Parent().Next().Text())

	email := doc.Find("i.glyphicon.glyphicon-envelope").Parent().Next().Children().Text()

	var location *Location
	gmaps := doc.Find("script")
	if gmaps.Length() > 0 {
		gmapsText := gmaps.Text()
		re = regexp.MustCompile(`lat: [0-9]+\.[0-9]+`)
		latitude := re.FindString(gmapsText)[5:]
		re = regexp.MustCompile(`lng: [0-9]+\.[0-9]+`)
		longitude := re.FindString(gmapsText)[5:]
		location = &Location{Latitude: latitude, Longitude: longitude}
	}
	// öffnungszeiten glyphicon glyphicon-time
	return &Canteen{
		Name:     name,
		Address:  address,
		City:     "Berlin",
		Phone:    phone,
		Email:    email,
		Location: location,
		Feeds:    []Feed{Feed{Name: "full", Url: urlFeedBase + id + "/full.xml"}},
	}
}

func getDay(id, date string) (d Day) {
	d.Date = date
	doc := getHttpDoc(urlBase+urlMeal, url.Values{"resources_id": {id}, "date": {date}})

	categories := doc.Find("div.splGroupWrapper")
	if categories.Length() == 1 && categories.Find("div").Length() == 0 && strings.TrimSpace(categories.Find("br").Text()) == "Kein Speisenangebot" {
		return
	}
	// loop over categories
	categories.EachWithBreak(func(i int, s *goquery.Selection) bool {
		// check if no meals are served
		if i < 1 && s.Find("div").Length() == 0 && strings.TrimSpace(s.Find("br").Text()) == "Kein Speiseangebot" {
			log.Println("INFO:", id, date, "kein Speiseangebot")
			return false
		}
		c := Category{Name: strings.TrimSpace(s.Find("div.splGroup").Text())}

		// loop over meals
		s.Find("div.splMeal").Each(func(i int, s *goquery.Selection) {
			m := Meal{Name: strings.TrimSpace(s.Find("span.bold").Text())}

			// prices: if only one price tag only for other
			prices := s.Find("div.text-right").Text()
			pricesRoles := [...]string{"student", "employee", "other"}
			pricesSplit := strings.SplitN(prices, "/", 3)
			for j := len(pricesSplit); j > 0; j-- {
				p := Price{
					Price: strings.Replace(strings.Trim(pricesSplit[len(pricesSplit)-j], " \n\t\r€"), ",", ".", 1),
					Role:  pricesRoles[len(pricesRoles)-j],
				}
				m.Prices = append(m.Prices, p)
			}

			// notes from icons
			notesImg := map[string]Note{
				"ampel_gruen_70x65.png": "Grün (Ampel)",
				"ampel_gelb_70x65.png":  "Gelb (Ampel)",
				"ampel_rot_70x65.png":   "Rot (Ampel)",
				"15.png":                "vegan",
				"43.png":                "Klimaessen",
				"1.png":                 "vegetarisch",
				"18.png":                "bio",
				"38.png":                "MSC",
			}
			s.Find("img.splIcon").Each(func(i int, s *goquery.Selection) {
				imgUrl := s.AttrOr("src", "")
				for suffix, note := range notesImg {
					if strings.HasSuffix(imgUrl, suffix) {
						m.Notes = append(m.Notes, note)
						break
					}
				}
			})

			// notes from text
			s.Find("div.kennz td").Not("td.text-right").Each(func(i int, s *goquery.Selection) {
				m.Notes = append(m.Notes, Note(s.Text()))
			})

			c.Meals = append(c.Meals, m)
		})

		d.Categories = append(d.Categories, c)
		return true
	})
	return
}

func getMeals(id string, daysBefore, daysAfter int) (c *Canteen) {
	c = &Canteen{}
	now := time.Now()

	for i := daysBefore; i <= daysAfter; i++ {
		date := now.AddDate(0, 0, i).Format("2006-01-02")
		c.Days = append(c.Days, getDay(id, date))
	}

	return
}

func main() {
	// generate index.json
	log.Println("generate", indexFile, "(index)")
	file, err := os.Create(indexFile)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	genIndex(file)

	ids := getIDs()
	// generate metadata files
	for _, id := range ids {
		filename := metadataBase + id + ".xml"
		log.Println("generate", filename, "(metadata)")
		file, err := os.Create(filename)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := getMetadata(id).Write(file); err != nil {
			log.Fatal(err)
		}
	}

	// full feed
	for _, id := range ids {
		path := feedBase + id
		if err := os.MkdirAll(path, os.ModePerm); err != nil {
			log.Fatal(err)
		}

		filename := path + "/full.xml"
		log.Println("generate", filename, "(feed full)")

		file, err := os.Create(filename)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := getMeals(id, -1, 21).Write(file); err != nil {
			log.Fatal(err)
		}
	}
}
