package main

import (
	"encoding/xml"
	"io"
)

const (
	xmlHeader = xml.Header + `<openmensa version="2.1"
           xmlns="http://openmensa.org/open-mensa-v2"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xsi:schemaLocation="http://openmensa.org/open-mensa-v2 http://openmensa.org/open-mensa-v2.xsd">` + "\n"
	xmlFooter = "\n</openmensa>\n"
)

type FeedSchedule struct {
	DayOfMonth string `xml:"dayOfMonth,attr,omitempty"`
	DayOfWeek  string `xml:"dayOfWeek,attr,omitempty"`
	Month      string `xml:"month,attr,omitempty"`
	Hour       string `xml:"hour,attr"`
	Minute     string `xml:"minute,attr,omitempty"`
	Retry      string `xml:"retry,attr,omitempty"`
}

type Feed struct {
	XMLName  xml.Name      `xml:"feed"`
	Name     string        `xml:"name,attr"`
	Priority int           `xml:"priority,attr,omitempty"`
	Schedule *FeedSchedule `xml:"schedule,omitempty"`
	Url      string        `xml:"url"`
	Source   string        `xml:"source,omitempty"`
}

type Location struct {
	Latitude  string `xml:"latitude,attr"`
	Longitude string `xml:"longitude,attr"`
}

type Price struct {
	XMLName xml.Name `xml:"price"`
	Price   string   `xml:",chardata"`
	Role    string   `xml:"role,attr"`
}
type Note string

type Meal struct {
	XMLName xml.Name `xml:"meal"`
	Name    string   `xml:"name"`
	Notes   []Note   `xml:"note"`
	Prices  []Price
}

type Category struct {
	XMLName xml.Name `xml:"category"`
	Name    string   `xml:"name,attr"`
	Meals   []Meal
}

func (c *Category) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	// only output if there are meals
	if len(c.Meals) == 0 {
		// TODO: maybe log this case?
		return nil
	}
	start.Name = xml.Name{Local: "category"}
	start.Attr = []xml.Attr{xml.Attr{Name: xml.Name{Local: "name"}, Value: c.Name}}

	err := e.EncodeToken(start)
	if err != nil {
		return err
	}
	for _, m := range c.Meals {
		err = e.Encode(m)
		if err != nil {
			return err
		}
	}

	err = e.EncodeToken(start.End())
	if err != nil {
		return err
	}
	return e.Flush()
}

type Day struct {
	Date       string `xml:"date"`
	Categories []Category
}

func (d *Day) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "day"}
	start.Attr = []xml.Attr{xml.Attr{Name: xml.Name{Local: "date"}, Value: d.Date}}

	// check if at least one meal exists
	closed := true
	for _, c := range d.Categories {
		if len(c.Meals) > 0 {
			closed = false
			break
		}
	}

	err := e.EncodeToken(start)
	if err != nil {
		return err
	}

	if closed {
		err := e.Encode(struct {
			XMLName xml.Name `xml:"closed"`
		}{})
		if err != nil {
			return err
		}
	} else {
		for _, c := range d.Categories {
			err = e.Encode(c)
			if err != nil {
				return err
			}
		}
	}

	err = e.EncodeToken(start.End())
	if err != nil {
		return err
	}
	return e.Flush()
}

type Availability string

func (a Availability) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if string(a) == "" {
		return nil
	}

	return e.EncodeElement(string(a), start)
}

type Times struct {
	openingHours []string
}

func (times Times) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if len(times.openingHours) == 0 {
		return e.EncodeElement("", start)
	} else if len(times.openingHours) != 7 {
		panic("len(times.openingHours) != 7 and not empty")
	}

	start = xml.StartElement{
		Name: xml.Name{"", "times"},
		Attr: []xml.Attr{xml.Attr{Name: xml.Name{"", "type"}, Value: "opening"}},
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}

	for i, name := range [7]string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		var attr xml.Attr
		if times.openingHours[i] == "" {
			attr = xml.Attr{Name: xml.Name{"", "closed"}, Value: "true"}
		} else {
			attr = xml.Attr{Name: xml.Name{"", "open"}, Value: times.openingHours[i]}
		}
		startDay := xml.StartElement{
			Name: xml.Name{"", name},
			Attr: []xml.Attr{attr},
		}
		if err := e.EncodeElement("", startDay); err != nil {
			return err
		}
	}

	return e.EncodeToken(start.End())
}

type Canteen struct {
	XMLName      xml.Name     `xml:"canteen"`
	Name         string       `xml:"name,omitempty"`
	Address      string       `xml:"address,omitempty"`
	City         string       `xml:"city,omitempty"`
	Phone        string       `xml:"phone,omitempty"`
	Email        string       `xml:"email,omitempty"`
	Location     *Location    `xml:"location,omitempty"`
	Availability Availability `xml:"availability,omitemtpy"`
	Times        *Times       `xml:"times,omitemtpy"`
	Feeds        []Feed       `xml:",omitempty"`
	Days         []Day
}

func (c *Canteen) Write(w io.Writer) error {
	if _, err := io.WriteString(w, xmlHeader); err != nil {
		return err
	}

	enc := xml.NewEncoder(w)
	enc.Indent("  ", "  ")
	if err := enc.Encode(c); err != nil {
		return err
	}

	_, err := io.WriteString(w, xmlFooter)
	return err
}

/*
func xmlTest() {
	enc := xml.NewEncoder(os.Stdout)
	enc.Indent("", "    ")

	//	v := &Meal{Prices: []Price{Price{Price: "1.55", Role: "student"}}, Annotations: []Annotation{"student", "vegan"}}
	//	v2 := &Meal{Name: "nla"} //Prices: []Price{Price{Price: "1.55", Role: "student"}}, Annotations: []Annotation{"student", "vegan"}}

	v := &Day{Date: "2016-01-01"}
	v2 := &Category{
		Name: "cat2",
		Meals: []Meal{
			Meal{
				Name: "meal21",
				Prices: []Price{
					Price{
						Price: "1.55",
						Role:  "student",
					},
				},
				Notes: []Note{
					"!",
					"!!!!",
				},
			},
			Meal{
				Name: "meal22",
				Notes: []Note{
					"r",
					"rrr!",
				},
			},
		},
	}

	enc.Encode(v)
	enc.Encode(v2)
}

*/
