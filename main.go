package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/mpvl/unique"
)

const (
	urlBase   = "https://www.stw.berlin/xhr/"
	urlMeta   = urlBase + "speiseplan-und-standortdaten.html"
	urlMeal   = urlBase + "speiseplan-wochentag.html"
	defaultID = "321" // Mensa TU

	urlFeedBase = "https://raw.githubusercontent.com/escrl/openmensa-feed-berlin/master/"

	repo           = "berlin/"
	idsArchiveFile = repo + "ids_archive"
	idsAllFile     = repo + "ids_all"
	idsCurFile     = repo + "ids_current"
	indexFile      = repo + "index.json"

	httpMaxRetries = 10
	httpSleepStep  = time.Second
)

func getHttpDoc(url string, data url.Values) *goquery.Document {
	for i := 1; i <= httpMaxRetries; i++ {
		resp, err := http.PostForm(url, data)
		if err != nil {
			log.Println(resp)
			log.Println(err)
			// panic(err)
			sleepTime := time.Duration(i) * httpSleepStep
			time.Sleep(sleepTime)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			doc, err := goquery.NewDocumentFromResponse(resp)
			if err != nil {
				panic(err)
			}
			return doc
		}
		// not bandwidth limit exceeded (inofficial)
		if resp.StatusCode == 509 { //|| resp.StatusCode == 500 {
			sleepTime := time.Duration(i) * httpSleepStep
			time.Sleep(sleepTime)
		} else {
			log.Printf("%s: got status code %d\n", url, resp.StatusCode)
			return nil
		}
	}
	log.Printf("aborting after %d retries for POST fetch at %s with %s", httpMaxRetries, url, data)
	return nil
}

func fetchIds() []string {
	doc := getHttpDoc(urlMeta, url.Values{"resources_id": {defaultID}})

	list := doc.Find("select#listboxEinrichtungen.listboxStandorte option[value]")
	ids := make([]string, list.Length())

	list.Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("value")
		ids[i] = id
	})
	return ids
}

func getMetadata(id string) *Canteen {
	doc := getHttpDoc(urlMeta, url.Values{"resources_id": {id}})

	name := strings.TrimSpace(doc.Find("select#listboxEinrichtungen.listboxStandorte option[selected]").Text())

	// use direct link instead
	if name == "" {
		directLink := doc.Find("div#directlink").Text()

		// use iframe from mensatogo instead
		if directLink == "" {
			iframe, _ := doc.Find("iframe").Attr("src")

			if iframe == "" {
				//name = strings.TrimSpace(doc.Find("h2").First().Text())
				log.Printf("%s: unable to determine name\n", id)
			} else {
				doc2 := getHttpDoc(iframe, nil)

				re := regexp.MustCompile(`mensa=(\d*)`)
				if m := re.FindStringSubmatch(iframe); m == nil {
					log.Printf("%s: unable to determine name with mensatogo method\n", id)
				} else {
					// TODO: does not respect escaped \"
					re, err := regexp.Compile(`var locations = JSON\.parse\(.*"` + m[1] + `":("[^"]*")`)
					if err != nil {
						log.Fatal(err)
					}
					m = re.FindStringSubmatch(doc2.Find("script").Text())
					if m == nil {
						log.Printf("%s: unable to determine name with mensatogo method\n", id)
					} else {
						dec := json.NewDecoder(strings.NewReader(m[1]))
						if err := dec.Decode(&name); err != nil {
							log.Fatal(err)
						}
						name = strings.TrimSpace(name)
						log.Printf("%s: name `%s` determined with mensatogo method\n", id, name)
					}
				}
			}
		} else {
			doc2 := getHttpDoc(directLink, nil)
			if doc2 != nil {
				name = doc2.Find("title").Text()
				name = name[len("studierendenWERK BERLIN - "):]
				log.Printf("%s: name `%s` determined with directlink method\n", id, name)
			} else {
				log.Printf("%s: unable to determine name", id)
			}
		}
	}

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
		re = regexp.MustCompile(`fromLonLat\(\[ (?P<longitude>-?\d+\.\d+), (?P<latitude>-?\d+\.\d+)`)
		if m := re.FindStringSubmatch(osm.Text()); m == nil {
			log.Printf("%s: %s: did not find location coordinates within \"%s\"\n", id, name, osm.Text())
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
		Name:         name,
		Address:      address,
		City:         "Berlin",
		Phone:        phone,
		Email:        email,
		Location:     location,
		Availability: "public",
		Times:        &Times{openingHours: openingHours},
		Feeds: []Feed{Feed{
			Name:     "full",
			Schedule: &FeedSchedule{Hour: "8", Retry: "45 3 1440"},
			Url:      urlFeedBase + id + "/full.xml",
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
				log.Printf("%s: %s: %s: encoutered an meal without a name tag\n", id, date, c.Name)
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
				log.Printf("%s: %s: did find %s prices but expected 0, 1 or 3 within \"%s\"\n", id, name, len(m), prices)
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

func genIndex(idsCur, idsArchived []string) error {
	log.Println("generate", indexFile, "(index)")

	file, err := os.Create(indexFile)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "{\n")
	if err != nil {
		return err
	}
	for _, id := range idsCur {
		jsonId, _ := json.Marshal(id)
		jsonUrl, _ := json.Marshal(urlFeedBase + id + "/metadata.xml")
		_, err = fmt.Fprintf(file, "    %s: %s,\n", jsonId, jsonUrl)
		if err != nil {
			return err
		}
	}
	file.Seek(-2, os.SEEK_CUR)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "\n}\n")
	return err
}

func sortStringInts(list []string) {
	sort.Slice(list, func(i, j int) bool {
		a, _ := strconv.Atoi(list[i])
		b, _ := strconv.Atoi(list[j])
		return a < b
	})

}

func saveIds(ids *[]string, filename string) error {
	log.Println("generate", filename)
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	w := bufio.NewWriter(file)
	for _, id := range *ids {
		if strings.ContainsRune(id, '\n') {
			return errors.New("Id contains newline")
		}
		fmt.Fprintln(w, id)
	}
	return w.Flush()
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func loadIds(filename string) ([]string, error) {
	if _, err := os.Stat(filename); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	log.Println("restore IDs from", filename)
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		ids = append(ids, scanner.Text())
	}
	return ids, scanner.Err()
}

// set difference (b \ a) of two sorted lists
func diff(a, b []string) []string {
	diff := make([]string, len(b))

	i, j, k := 0, 0, 0
	for i < len(a) && j < len(b) {
		if a[i] < b[j] {
			i++
		} else if a[i] > b[j] {
			diff[k] = b[j]
			k++
			j++
		} else {
			i++
			j++
		}
	}
	for ; j < len(b); j++ {
		diff[k] = b[j]
		k++
	}
	return diff[:k]
}

func main() {
	idsCur := fetchIds()
	unique.Sort(unique.StringSlice{&idsCur})

	idsAll, err := loadIds(idsAllFile)
	if err != nil {
		log.Fatal(err)
	}
	idsAll = append(idsAll, idsCur...)
	unique.Sort(unique.StringSlice{&idsAll})

	idsArchive := diff(idsCur, idsAll)

	err = saveIds(&idsCur, idsCurFile)
	if err != nil {
		log.Fatal(err)
	}
	err = saveIds(&idsArchive, idsArchiveFile)
	if err != nil {
		log.Fatal(err)
	}
	err = saveIds(&idsAll, idsAllFile)
	if err != nil {
		log.Fatal(err)
	}

	err = genIndex(idsCur, idsArchive)
	if err != nil {
		log.Fatal(err)
	}

	// generate metadata files
	for _, id := range idsCur {
		path := repo + id
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
	for _, id := range idsCur {
		path := repo + id
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
