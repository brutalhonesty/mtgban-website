package main

import (
	"os"
	"log"
	"net/http"
	"strconv"
	"encoding/json"
	"io/ioutil"
)

var sitesInput []byte;

func Explore(w http.ResponseWriter, r *http.Request) {
<<<<<<< Updated upstream
	sig := getSignatureFromCookies(r)
=======

	sig := r.FormValue("sig")
>>>>>>> Stashed changes

	pageVars := genPageNav("Explore", sig)

	// read our opened jsonFile as a byte array.
	// TODO Move this into a DB.
	if sitesInput == nil {
		jsonFile, err := os.Open("newList.json")

		if err != nil {
	    	log.Println(err)
	    	pageVars.Title = "Great things are coming"
			pageVars.ErrorMessage = "Ran into an issue, let the admins know."

			render(w, "explore.html", pageVars)
			return
		}

		sitesInput, _ := ioutil.ReadAll(jsonFile)


		// we unmarshal our byteArray which contains our
		// jsonFile's content into 'users' which we defined above
		json.Unmarshal(sitesInput, &pageVars.Sites)
	}

	if !DatabaseLoaded {
		pageVars.Title = "Great things are coming"
		pageVars.ErrorMessage = "Website is starting, please try again in a few minutes"

		render(w, "explore.html", pageVars)
		return
	}

	exploreParam, _ := GetParamFromSig(sig, "Explore")
	canExplore, _ := strconv.ParseBool(exploreParam)
	if SigCheck && !canExplore {
		pageVars.Title = "This feature is BANned"
		pageVars.ErrorMessage = ErrMsgPlus
		pageVars.ShowPromo = true

		render(w, "explore.html", pageVars)
		return
	}

	q := r.FormValue("q")

	pageVars.SearchQuery = q
	for _, site := range pageVars.Sites {
		if site.SiteCategory == "BB" {
			pageVars.BBSellers = append(pageVars.BBSellers, site)
		}
		if site.SiteCategory == "CAN" {
			pageVars.CANSellers = append(pageVars.CANSellers, site)
		}
		if site.SiteCategory == "USA" {
			pageVars.USASellers = append(pageVars.USASellers, site)
		}
		if site.SiteCategory == "JPN" {
			pageVars.JPNSellers = append(pageVars.JPNSellers, site)
		}
	}

	if q != "" {
		render(w, "explore.html", pageVars)
		return
	}

	// TODO separate by types and move this up before we render.
	enabled, _ := GetParamFromSig(sig, "ExpEnabled")
	switch enabled {
	case "ALL":
	case "FULL":
	case "MOST":
	case "ENTRY":
	case "DEMO":
	default:
		pageVars.Title = "This feature is BANned"
		pageVars.ErrorMessage = ErrMsgPlus

		render(w, "explore.html", pageVars)
		return
	}

	render(w, "explore.html", pageVars)
	return
}
