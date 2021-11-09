package main

import (
	"encoding/json"
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
	urlMeta   = urlBase + "speiseplan-und-standortdaten.html"
	urlMeal   = urlBase + "speiseplan-wochentag.html"
	defaultID = "321" // Mensa TU

	urlFeedBase = "https://raw.githubusercontent.com/escrl/openmensa-feed-berlin/master/"

	repo      = "berlin/"
	idsFile   = repo + "ids.json"
	indexFile = repo + "index.json"

	httpMaxRetries = 10
	httpSleepStep  = time.Second
)

var ids map[string]string

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
			//log.Printf("sleep for %s before doing POST fetch at %s with %s", sleepTime, url, data)
			time.Sleep(sleepTime)
		} else {
			log.Printf("aborting after %s retries for POST fetch at %s with %s", httpMaxRetries, url, data)
		}
	}
	return
}

// id -> nice name
func fetchIDs() map[string]string {
	doc := getHttpDoc(urlMeta, url.Values{"resources_id": {defaultID}})

	list := doc.Find("select#listboxEinrichtungen.listboxStandorte option[value]")
	ids := make(map[string]string, list.Length())

	list.Each(func(i int, s *goquery.Selection) {
		name := s.Text()

		// generate a name which matches [a-z0-9_], handle [äüößé] nicely
		safeName := make([]byte, len(name))
		j := 0
		for _, c := range name {
			if 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
				safeName[j] = byte(c)
				j++
			} else if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
				safeName[j] = byte(c)
				j++
			} else if c == 'ä' {
				safeName[j] = 'a'
				safeName[j+1] = 'e'
				j += 2 // this is safe to do as 'ä' is encode in two bytes
			} else if c == 'ö' {
				safeName[j] = 'o'
				safeName[j+1] = 'e'
				j += 2
			} else if c == 'ü' {
				safeName[j] = 'u'
				safeName[j+1] = 'e'
				j += 2
			} else if c == 'ß' {
				safeName[j] = 's'
				safeName[j+1] = 's'
				j += 2
			} else if c == 'é' {
				safeName[j] = 'e'
				j++
			} else if j > 0 && safeName[j-1] != '_' {
				safeName[j] = '_'
				j++
			}
		}
		if j > 0 && safeName[j-1] == '_' {
			j--
		}
		id, _ := s.Attr("value")
		ids[id] = string(safeName[:j])
	})
	return ids
}

func getMetadata(id string) *Canteen {
	doc := getHttpDoc(urlMeta, url.Values{"resources_id": {id}})

	name := doc.Find("select#listboxEinrichtungen.listboxStandorte option[selected]").Text()

	address := doc.Find("i.glyphicon.glyphicon-map-marker").Parent().Next().Text()
	re := regexp.MustCompile(`\(Bezirk.*\)`)
	address = re.ReplaceAllString(address, "")
	re = regexp.MustCompile(`\b.*\b`)
	address = strings.Join(re.FindAllString(address, -1), ", ")

	phone := strings.TrimSpace(doc.Find("i.glyphicon.glyphicon-earphone").Parent().Next().Text())

	email := doc.Find("i.glyphicon.glyphicon-envelope").Parent().Next().Children().Text()

	source := doc.Find("div#directlink").Text()

	var location *Location
	osm := doc.Find("script")
	if osm.Length() > 0 {
		re = regexp.MustCompile(`fromLonLat\(\[ (?P<longitude>\d+\.\d+), (?P<latitude>\d+\.\d+)`)
		if m := re.FindStringSubmatch(osm.Text()); m == nil {
			log.Printf("%s: %s: did not find location coordinatesopening within \"%s\"\n", ids[id], name, osm.Text())
		} else {
			location = &Location{Longitude: m[1], Latitude: m[2]}
		}
	}

	days := []string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}
	openingHours := make([]string, 7)

	times := doc.Find("i.glyphicon.glyphicon-time").Parent().Parent().Next()
	re = regexp.MustCompile(`(?P<dayStart>[DFMS][aior])\.(?: – (?P<dayEnd>[DFMS][aior])\.)?.*\n.*(?P<hoursStart>\d{2}:\d{2}) – (?P<hoursEnd>\d{2}:\d{2}) Uhr`)
	for i := 0; i < len(days); i++ {
		m := re.FindStringSubmatch(times.Text())
		if len(m) == 0 {
			break
		}

		var dayStart, dayEnd int
		for j, day := range days {
			if m[re.SubexpIndex("dayStart")] == day {
				dayStart = j
				break
			}
		}
		if m[re.SubexpIndex("dayEnd")] == "" {
			dayEnd = dayStart
		} else {
			for j, day := range days {
				if m[re.SubexpIndex("dayEnd")] == day {
					dayEnd = j
					break
				}
			}
		}
		if dayEnd < dayStart {
			panic("dayEnd < dayStart")
		}

		for j := dayStart; j <= dayEnd; j++ {
			openingHours[j] = strings.Join(m[re.SubexpIndex("hoursStart"):], "-")
		}

		times = times.Next()
	}

	return &Canteen{
		Name:     name,
		Address:  address,
		City:     "Berlin",
		Phone:    phone,
		Email:    email,
		Location: location,
		Pub:      PubliclyAvailable(true),
		Times:    &Times{openingHours: openingHours},
		Feeds: []Feed{Feed{
			Name:     "full",
			Schedule: &FeedSchedule{Hour: "8", Retry: "45 3 1440"},
			Url:      urlFeedBase + ids[id] + "/full.xml",
			Source:   source,
		}},
	}
}

func getDay(id, date string) (d Day) {
	d.Date = date
	doc := getHttpDoc(urlMeal, url.Values{"resources_id": {id}, "date": {date}})

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
			name := strings.TrimSpace(s.Find("span.bold").Text())
			if len(name) == 0 {
				log.Printf("%s: %s: %s: encoutered an meal without a name tag\n", ids[id], date, c.Name)
				name = "N. N."
			}
			meal := Meal{Name: name}

			// prices: if only one price tag is present only use it for 'other'
			prices := strings.TrimSpace(s.Find("div.text-right").Text())

			re := regexp.MustCompile(`\d+,\d{2}`)
			m := re.FindAllString(prices, -1)
			switch len(m) {
			case 0:
				// regularly the case for slat dressing, so do not log
				// log.Printf("%s: %s: did not find prices within \"%s\"\n", ids[id], name, prices)
			case 1:
				meal.Prices = []Price{Price{
					Price: strings.Replace(m[0], ",", ".", 1),
					Role:  "other",
				}}
			case 3:
				meal.Prices = make([]Price, len(m))
				pricesRoles := [...]string{"student", "employee", "other"}
				for j, price := range m {
					meal.Prices[j] = Price{
						Price: strings.Replace(price, ",", ".", 1),
						Role:  pricesRoles[j],
					}
				}
			default:
				log.Printf("%s: %s: did find %s prices but expected 0, 1 or 3 within \"%s\"\n", ids[id], name, len(m), prices)
			}

			// notes from icons
			notesImg := map[string]Note{
				"ampel_gruen_70x65.png": "grün (Ampel)",
				"ampel_gelb_70x65.png":  "gelb (Ampel)",
				"ampel_rot_70x65.png":   "rot (Ampel)",
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
						meal.Notes = append(meal.Notes, note)
						break
					}
				}
			})

			// notes from text
			s.Find("div.kennz td").Not("td.text-right").Each(func(i int, s *goquery.Selection) {
				meal.Notes = append(meal.Notes, Note(s.Text()))
			})

			c.Meals = append(c.Meals, meal)
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

func updateIDs() {
	if _, err := os.Stat(idsFile); os.IsNotExist(err) {
		log.Println("generate", idsFile)
	} else {
		log.Println("update", idsFile)
		if err = os.Rename(idsFile, idsFile+".old"); err != nil {
			log.Fatal(err)
		}
	}

	file, err := os.Create(idsFile)
	defer file.Close()
	if err != nil {
		log.Fatal(err)
	}

	enc := json.NewEncoder(file)
	if err := enc.Encode(ids); err != nil {
		log.Fatal(err)
	}
}

func updateIndex() {
	if _, err := os.Stat(indexFile); os.IsNotExist(err) {
		log.Println("generate", indexFile, "(index)")
	} else {
		log.Println("update", indexFile, "(index)")
		if err = os.Rename(indexFile, indexFile+".old"); err != nil {
			log.Fatal(err)
		}
	}

	file, err := os.Create(indexFile)
	defer file.Close()
	if err != nil {
		log.Fatal(err)
	}
	index := map[string]string{}

	for _, name := range ids {
		index[name] = urlFeedBase + name + "/metadata.xml"
	}

	enc := json.NewEncoder(file)
	if err := enc.Encode(index); err != nil {
		log.Fatal(err)
	}
}

func restoreIDs() {
	log.Println("restore IDs and names from", idsFile)
	file, err := os.Open(idsFile)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	dec := json.NewDecoder(file)
	if err := dec.Decode(&ids); err != nil {
		log.Fatal(err)
	}
}

func main() {
	if len(os.Args) > 2 {
		log.Fatal("to many commandline arguments")
	} else if len(os.Args) == 2 {
		// update ids and index files
		if strings.Compare(os.Args[1], "-u") == 0 {
			ids = fetchIDs()
			updateIDs()
			updateIndex()
			os.Exit(0)
		} else {
			log.Fatal("unkown commandline argument")
		}
	}

	// run without any arguments
	restoreIDs()
	//ids = map[string]string{defaultID: ids[defaultID]} //TODO debug
	//id := "367" // hu_süd
	//ids = map[string]string{id: ids[id]} //TODO debug

	// generate metadata files
	for id, name := range ids {
		path := repo + name
		if err := os.MkdirAll(path, os.ModePerm); err != nil {
			log.Fatal(err)
		}
		filename := path + "/metadata.xml"
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
	for id, name := range ids {
		path := repo + name
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
