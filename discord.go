package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	cleanhttp "github.com/hashicorp/go-cleanhttp"

	"github.com/kodabb/go-mtgban/mtgmatcher"
)

var poweredByFooter = discordgo.MessageEmbedFooter{
	IconURL: "https://www.mtgban.com/img/logo/ban-round.png",
	Text:    "Powered by mtgban.com",
}

const (
	// Avoid making messages overly long
	MaxPrintings = 12

	// Overflow prevention for field.Value size
	MaxCustomEntries = 7

	// Discord API constants
	MaxEmbedFieldsValueLength = 1024
	MaxEmbedFieldsNumber      = 25
)

func setupDiscord() error {
	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + Config.DiscordToken)
	if err != nil {
		return err
	}

	// Register the guildCreate func as a callback for GuildCreat events
	dg.AddHandler(guildCreate)

	// Register the messageCreate func as a callback for MessageCreate events.
	dg.AddHandler(messageCreate)

	// In this example, we only care about receiving message events.
	dg.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsGuilds | discordgo.IntentsGuildMessages)

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		return err
	}

	return nil
	// Cleanly close down the Discord session.
	//dg.Close()
}

// This function will be called every time the bot is invited to a discord
// server and tries to join it.
func guildCreate(s *discordgo.Session, gc *discordgo.GuildCreate) {
	// If guild is authorized, then we can proceed as normal
	if stringSliceContains(Config.DiscordAllowList, gc.Guild.ID) {
		return
	}

	// Otherwise we print a message, pick our stuff, and leave
	s.ChannelMessageSendEmbed(gc.Guild.SystemChannelID, &discordgo.MessageEmbed{
		Description: "Looks like I'm not authorized to be here ⋋〳 ᵕ _ʖ ᵕ 〵⋌",
		Footer:      &poweredByFooter,
	})
	Notify("bot", gc.Guild.Name+" attempted to install me ▐ ✪ _ ✪▐")
	s.GuildLeave(gc.Guild.ID)
}

type searchResult struct {
	Invalid         bool
	CardId          string
	ResultsSellers  []SearchEntry
	ResultsVendors  []SearchEntry
	EditionSearched string
	SearchQuery     string
}

func parseMessage(content string) (*searchResult, error) {
	// Clean up query and only search for NM
	query, options := parseSearchOptions(content)

	// Set a custom search mode since we want to try and find as much as possible
	if options["search_mode"] == "" {
		options["search_mode"] = "any"
	}

	// Prevent useless invocations
	if len(query) < 3 && query != "Ow" && query != "X" {
		return &searchResult{Invalid: true}, nil
	}

	// Check if card exists
	var nameFound string
	sets := mtgmatcher.GetSets()
	if options["edition"] != "" {
		set, found := sets[options["edition"]]
		if !found {
			return nil, fmt.Errorf("No card found named \"%s\" in \"%s\" 乁| ･ิ ∧ ･ิ |ㄏ", query, options["edition"])
		}
		for _, card := range set.Cards {
			if mtgmatcher.Contains(card.Name, query) {
				nameFound = card.Name
				break
			}
		}
		if nameFound == "" {
			return nil, fmt.Errorf("No card found named \"%s\" in %s 乁| ･ิ ∧ ･ิ |ㄏ", query, set.Name)
		}
	}
	if nameFound == "" {
		for _, set := range sets {
			for _, card := range set.Cards {
				if mtgmatcher.Contains(card.Name, query) {
					nameFound = card.Name
					break
				}
			}
			if nameFound != "" {
				break
			}
		}
	}
	if nameFound == "" {
		return nil, fmt.Errorf("No card found for \"%s\" 乁| ･ิ ∧ ･ิ |ㄏ", query)
	}

	// Search both sellers and vendors
	var cardId, cardIdV string
	var resultsSellers, resultsVendors []SearchEntry
	var errS, errV error
	var wg sync.WaitGroup
	wg.Add(2)

	// For Sellers, only consider NM entries
	options["condition"] = "NM"

	go func() {
		cardId, resultsSellers, errS = searchSellersFirstResult(query, options)
		wg.Done()
	}()
	go func() {
		cardIdV, resultsVendors, errV = searchVendorsFirstResult(query, options)
		wg.Done()
	}()

	wg.Wait()
	switch {
	// Both errored, card is oos
	case errS != nil && errV != nil:
		return nil, errS
	// Retail is oos, but buylist isn't, let's use it
	case errS != nil:
		cardId = cardIdV
	// Buylist is not oos, but it returned a different card id,
	// which means the original one is actually oos
	case errV == nil && cardId != cardIdV:
		resultsVendors = []SearchEntry{}
	}

	// Rebuild the search query
	searchQuery := nameFound
	if options["edition"] != "" {
		searchQuery += " s:" + options["edition"]
	}
	if options["number"] != "" {
		searchQuery += " cn:" + options["number"]
	}
	if options["foil"] != "" {
		searchQuery += " f:" + options["foil"]
	}

	return &searchResult{
		CardId:          cardId,
		ResultsSellers:  resultsSellers,
		ResultsVendors:  resultsVendors,
		EditionSearched: options["edition"],
		SearchQuery:     searchQuery,
	}, nil
}

type embedField struct {
	Name  string
	Value string
}

func search2fields(searchRes *searchResult) (fields []embedField) {
	// Add two embed fields, one for retail and one for buylist
	fieldsNames := []string{"Retail", "Buylist"}
	for i, results := range [][]SearchEntry{searchRes.ResultsSellers, searchRes.ResultsVendors} {
		field := embedField{
			Name: fieldsNames[i],
		}

		// Results look really bad after MaxCustomEntries, and too much info
		// does not help, so sort by best price, trim, then sort back to original
		if len(results) > MaxCustomEntries {
			if i == 0 {
				sort.Slice(results, func(i, j int) bool {
					return results[i].Price < results[j].Price
				})
			} else if i == 1 {
				sort.Slice(results, func(i, j int) bool {
					return results[i].Price > results[j].Price
				})
			}
			results = results[:MaxCustomEntries]
			sort.Slice(results, func(i, j int) bool {
				return results[i].ScraperName < results[j].ScraperName
			})
		}

		// Alsign to the longest name by appending whitespaces
		alignLength := longestName(results)
		for _, entry := range results {
			extraSpaces := ""
			for i := len(entry.ScraperName); i < alignLength; i++ {
				extraSpaces += " "
			}

			// Build url for our redirect
			kind := strings.ToLower(string(fieldsNames[i][0]))
			store := strings.Replace(entry.Shorthand, " ", "%20", -1)
			link := "https://" + DefaultHost + "/" + path.Join("go", kind, store, searchRes.CardId)

			// Set the custom field
			value := fmt.Sprintf("• **[`%s%s`](%s)** $%0.2f", entry.ScraperName, extraSpaces, link, entry.Price)
			if entry.Ratio > 60 {
				value += fmt.Sprintf(" 🔥")
			}
			if i == 1 {
				alarm := false
				for _, subres := range searchRes.ResultsSellers {
					if subres.Price < entry.Price {
						alarm = true
						break
					}
				}
				if alarm {
					value += fmt.Sprintf(" 🚨")
				}
			}
			value += "\n"

			// If we go past the maximum value for embed field values,
			// make a new field for any spillover, as long as we are within
			// the limits of the number of embeds allowed
			if len(field.Value)+len(value) > MaxEmbedFieldsValueLength && len(fields) < MaxEmbedFieldsNumber {
				fields = append(fields, field)
				field = embedField{
					Name: fieldsNames[i] + " (cont'd)",
				}
			}
			field.Value += value
		}
		if len(results) == 0 {
			field.Value = "N/A"
		}

		fields = append(fields, field)
	}

	return
}

type PriceEntry struct {
	Title    string  `json:"title"`
	Price    float64 `json:"price"`
	Shipping float64 `json:"shipping"`
}

func grabLastSold(tcgId string, foil bool) ([]embedField, error) {
	if tcgId == "" {
		return nil, errors.New("empty tcgId")
	}

	resp, err := cleanhttp.DefaultClient().Get("http://localhost:8081/" + tcgId)
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var entries map[string][]PriceEntry
	err = json.Unmarshal(data, &entries)
	if err != nil {
		log.Println(string(data))
		return nil, err
	}

	var fields []embedField
	var shipping []string
	var hasValues bool
	for i, entry := range entries["TCG Last Sold Listing"] {
		if foil && !strings.Contains(entry.Title, "Foil") {
			continue
		}

		value := "-"
		if entry.Price != 0 {
			hasValues = true
			value = fmt.Sprintf("$%0.2f", entry.Price)
			shipping = append(shipping, fmt.Sprintf("%0.2f", entry.Shipping))
		}
		fields = append(fields, embedField{
			Name:  entry.Title,
			Value: value,
		})

		if i == 4 || i == 9 {
			field := embedField{
				Name:  "Shipping",
				Value: strings.Join(shipping, " "),
			}
			if field.Value == "" {
				field.Value = "n/a"
			}
			fields = append(fields, field)
			shipping = []string{}
		}
	}

	// No prices received, this is not an error,
	// but print a message warning the user
	if !hasValues {
		log.Println("No last sold prices available")
		return nil, nil
	}

	return fields, nil
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the authenticated bot has access to.
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore requests if starting up
	if !DatabaseLoaded {
		return
	}

	// Ignore messages coming from unauthorized discords
	if !stringSliceContains(Config.DiscordAllowList, m.GuildID) {
		return
	}

	// Ignore all messages created by a bot
	if m.Author.Bot {
		return
	}

	// Ignore too short messages
	if len(m.Content) < 2 {
		return
	}

	// Parse message, look for bot command
	if !strings.HasPrefix(m.Content, "!") && !strings.HasPrefix(m.Content, "$$") {
		return
	}

	allBls := strings.HasPrefix(m.Content, "!")
	lastSold := strings.HasPrefix(m.Content, "$$")

	// Strip away beginning character
	content := strings.TrimPrefix(m.Content, "!")
	content = strings.TrimPrefix(content, "$$")

	// Search a single card match
	searchRes, err := parseMessage(content)
	if err != nil {
		s.ChannelMessageSendEmbed(m.ChannelID, &discordgo.MessageEmbed{
			Description: err.Error(),
		})
		return
	}
	if searchRes.Invalid {
		return
	}

	co, err := mtgmatcher.GetUUID(searchRes.CardId)
	if err != nil {
		return
	}

	// Convert search results into proper fields
	var fields []*discordgo.MessageEmbedField
	var ogFields []embedField
	if allBls {
		ogFields = search2fields(searchRes)
	} else if lastSold {
		ogFields, err = grabLastSold(co.Identifiers["tcgplayerProductId"], co.Foil)
		if err != nil {
			log.Println(err)
			return
		}
		if len(ogFields) == 0 {
			s.ChannelMessageSendEmbed(m.ChannelID, &discordgo.MessageEmbed{
				Description: "No Last Sold Price available for \"" + content + "\" o͡͡͡╮༼ • ʖ̯ • ༽╭o͡͡͡",
			})
			return
		}
	}
	for _, field := range ogFields {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   field.Name,
			Value:  field.Value,
			Inline: true,
		})
	}

	// Prepare card data
	card := uuid2card(searchRes.CardId, true)

	printings := strings.Join(co.Printings, ", ")
	if len(co.Printings) > MaxPrintings {
		printings = strings.Join(co.Printings[:MaxPrintings], ", ") + " and more"
	}
	if searchRes.EditionSearched != "" && len(co.Variations) > 0 {
		cn := []string{co.Number}
		for _, varid := range co.Variations {
			co, err := mtgmatcher.GetUUID(varid)
			if err != nil {
				continue
			}
			cn = append(cn, co.Number)
		}
		printings = fmt.Sprintf("%s. Variants in %s are %s", printings, searchRes.EditionSearched, strings.Join(cn, ", "))
	}

	link := "https://www.mtgban.com/search?q=" + url.QueryEscape(searchRes.SearchQuery) + "&utm_source=banbot&utm_affiliate=" + m.GuildID

	// Set title of the main message
	title := "Prices for " + card.Name
	if lastSold {
		title = "TCG Last Sold prices for " + card.Name
	}

	// Add a tag for ease of debugging
	if DevMode {
		title = "[DEV] " + title
	}
	// Spark-ly
	if card.Foil {
		title += " ✨"
	}

	embed := discordgo.MessageEmbed{
		Title:       title,
		Color:       0xFF0000,
		URL:         link,
		Description: fmt.Sprintf("[%s] %s\nPrinted in %s", card.SetCode, card.Title, printings),
		Fields:      fields,
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: card.ImageURL,
		},
		Footer: &discordgo.MessageEmbedFooter{},
	}

	// Some footer action, RL, stocks, powered by
	if card.Reserved {
		embed.Footer.Text = "Part of the Reserved List\n"
	}
	_, stocks := Infos["STKS"][searchRes.CardId]
	if stocks {
		embed.Footer.Text += "On MTGStocks Interests page\n"
	}
	// Show data source on non-ban servers
	if len(Config.DiscordAllowList) > 0 && m.GuildID != Config.DiscordAllowList[0] {
		embed.Footer.IconURL = poweredByFooter.IconURL
		embed.Footer.Text += poweredByFooter.Text
	}
	_, err = s.ChannelMessageSendEmbed(m.ChannelID, &embed)
	if err != nil {
		log.Println(err)
	}
}

// Obtain the length of the scraper with the longest name
func longestName(results []SearchEntry) (out int) {
	for _, entry := range results {
		probe := len(entry.ScraperName)
		if probe > out {
			out = probe
		}
	}
	return
}

// Retrieve cards from Sellers using the very first result
func searchSellersFirstResult(query string, options map[string]string) (cardId string, results []SearchEntry, err error) {
	// Search
	foundSellers, _ := searchSellers(query, append(Config.SearchBlockList, "TCG Direct"), options)
	if len(foundSellers) == 0 {
		err = errors.New("Out of stock everywhere ┻━┻ ヘ╰( •̀ε•́ ╰)")
		return
	}

	sortedKeysSeller := make([]string, 0, len(foundSellers))
	for cardId := range foundSellers {
		sortedKeysSeller = append(sortedKeysSeller, cardId)
	}
	if len(sortedKeysSeller) > 1 {
		sort.Slice(sortedKeysSeller, func(i, j int) bool {
			return sortSets(sortedKeysSeller[i], sortedKeysSeller[j])
		})
	}

	cardId = sortedKeysSeller[0]
	results = foundSellers[cardId]["NM"]

	if len(results) > 0 {
		// Drop duplicates by looking at the last one as they are alredy sorted
		tmp := append(results[:0], results[0])
		for i := range results {
			if results[i].ScraperName != tmp[len(tmp)-1].ScraperName {
				tmp = append(tmp, results[i])
			}
		}
		results = tmp
	}
	return
}

// Retrieve cards from Vendors using the very first result
func searchVendorsFirstResult(query string, options map[string]string) (cardId string, results []SearchEntry, err error) {
	foundVendors, _ := searchVendors(query, Config.SearchBlockList, options)
	if len(foundVendors) == 0 {
		err = errors.New("Nobody is buying that card ┻━┻ ヘ╰( •̀ε•́ ╰)")
		return
	}

	sortedKeysVendor := make([]string, 0, len(foundVendors))
	for cardId := range foundVendors {
		sortedKeysVendor = append(sortedKeysVendor, cardId)
	}
	if len(sortedKeysVendor) > 1 {
		sort.Slice(sortedKeysVendor, func(i, j int) bool {
			return sortSets(sortedKeysVendor[i], sortedKeysVendor[j])
		})
	}

	cardId = sortedKeysVendor[0]
	results = foundVendors[cardId]
	return
}
