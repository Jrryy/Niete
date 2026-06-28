package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/mattn/go-runewidth"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/net/html"
)

type individualRankingData struct {
	Level uint   `json:"level"`
	Rank  uint   `json:"rank"`
	Point uint64 `json:"point"`
}

type userRankingData struct {
	UserId         uint64                 `json:"user_id"`
	Name           string                 `json:"name"`
	MemberPosition uint                   `json:"member_position"`
	HasRanking     bool                   `json:"has_ranking"`
	Ranking        *individualRankingData `json:"ranking"`
}

type crewAPIData struct {
	MembersData    []userRankingData `json:"data"`
	Meta           any               `json:"meta"`
	RankingContext any               `json:"ranking_context"`
	Scheduled      any               `json:"schedules"`
}

var (
	discordToken, allowedChannels, translationForbiddenChannels, deeplKey, myCrew, ngrokPath, mcDirPath string
	mongoClient                                                                                         *mongo.Client
	ngrokProcess                                                                                        *os.Process
	logger                                                                                              log.Logger
	mcURLMessage                                                                                        *dgo.Message
	mcCmd                                                                                               *exec.Cmd
)

func intComma(i int) string {
	if i < 0 {
		return "-" + intComma(-i)
	}
	if i < 1000 {
		return fmt.Sprintf("%d", i)
	}
	return intComma(i/1000) + "," + fmt.Sprintf("%03d", i%1000)
}

func getDatabase() *mongo.Database {
	return mongoClient.Database("db")
}

func sendHelp(session *dgo.Session, channel string) error {
	// helpString := "```\n" +
	_ = "```\n" +
		"You know how this goes:\n" +
		"\t- $time: Display the current date and time in Japan.\n" +
		"\t- $spark: Show your stats (or creates your profile if it's your first time).\n" +
		"\t- $spark set [crystals|xtals|tickets|ticket|tix|10part] <number>: Set a new amount of pulls.\n" +
		"\t- $spark add [crystals|xtals|tickets|ticket|tix|10part] <number>: Add some amount to your pulls.\n" +
		"\t- $bless: Ask immunity Lily for her blessing before pulling (might and will go wrong).\n" +
		"\t- $gw <crew_name>: Retrieves past performances of the specified crew in GW.\n" +
		"```"
	// _, e := session.ChannelMessageSend(channel, helpString)
	return nil
}

func getXtalsTixAnd10Parts(data map[string]any) (int64, int64, int64) {
	xtals, ok := data["xtals"].(int64)
	if !ok {
		xtals32 := data["xtals"].(int32)
		xtals = int64(xtals32)
	}
	tix, ok := data["tix"].(int64)
	if !ok {
		tix32 := data["tix"].(int32)
		tix = int64(tix32)
	}
	tenPart, ok := data["10part"].(int64)
	if !ok {
		tenPart32 := data["10part"].(int32)
		tenPart = int64(tenPart32)
	}
	return xtals, tix, tenPart
}

func getTotalPulls(data map[string]any) int64 {
	xtals, tix, tenPart := getXtalsTixAnd10Parts(data)
	return xtals/300 + tix + tenPart*10
}

func sendPlayerData(session *dgo.Session, channel string, data map[string]any) (e error) {
	xtals, tix, tenPart := getXtalsTixAnd10Parts(data)
	totalPulls := xtals/300 + tix + tenPart*10
	var percentage float64 = 0
	if totalPulls > 0 {
		percentage = float64(totalPulls) / 3
	}
	fullBlocks := int(percentage) % 100
	lastBlockPercentage := math.Mod(percentage, 1) * 10
	var lastBlock string
	if lastBlockPercentage == 0 {
		lastBlock = " "
	} else if lastBlockPercentage < 3 {
		lastBlock = "▎"
	} else if lastBlockPercentage < 5 {
		lastBlock = "▌"
	} else if lastBlockPercentage < 8 {
		lastBlock = "▊"
	} else {
		lastBlock = "▉"
	}
	// playerDataString := fmt.Sprintf(
	_ = fmt.Sprintf(
		"```\n"+
			"%s\n"+
			"Crystals: %d\n"+
			"Tickets: %d\n"+
			"10 part tickets: %d\n"+
			"Total pulls saved: %d\n"+
			"[%s] %.2f%%\n"+
			"```",
		data["name"].(string),
		xtals,
		tix,
		tenPart,
		totalPulls,
		strings.Repeat("█", fullBlocks)+lastBlock+strings.Repeat(" ", 99-fullBlocks),
		percentage,
	)
	// _, e = session.ChannelMessageSend(channel, playerDataString)
	return
}

func createPlayerDocument(session *dgo.Session, channel string, discordId string, players *mongo.Collection) error {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	/*
		_, err := session.ChannelMessageSend(channel, "Profile not found. Creating...")
		if err != nil {
			return err
		}
	*/
	_, err := players.InsertOne(
		ctx,
		bson.M{"discordId": discordId, "xtals": 0, "tix": 0, "10part": 0},
	)
	return err
}

func createOrRetrievePlayerData(session *dgo.Session, channel string, discordId string, name string) error {
	var playerDataDict map[string]any
	collection := getDatabase().Collection("players")
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	result, err := collection.FindOne(ctx, bson.M{"discordId": discordId}).Raw()
	err = bson.Unmarshal(result, &playerDataDict)
	if err != nil {
		err = createPlayerDocument(session, channel, discordId, collection)
		return err
	}
	playerDataDict["name"] = name
	err = sendPlayerData(session, channel, playerDataDict)
	return err
}

func setQuantity(discordId, field string, quantity int, players *mongo.Collection) error {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	_, err := players.UpdateOne(ctx,
		bson.M{"discordId": discordId},
		bson.M{"$set": bson.M{field: quantity}},
	)
	return err
}

func addQuantity(discordId, field string, quantity int, players *mongo.Collection) error {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	_, err := players.UpdateOne(ctx,
		bson.M{"discordId": discordId},
		bson.M{"$inc": bson.M{field: quantity}},
	)
	return err
}

func parseSparkArgs(session *dgo.Session, args []string, channel string) (string, int, error) {
	if len(args) < 2 {
		// _, err := session.ChannelMessageSend(channel, "Specify correctly the kind of pulls you want to set.")
		return "", 0, nil // err
	} else {
		field := ""
		switch args[1] {
		case "crystals", "xtals":
			field = "xtals"
		case "10part":
			field = "10part"
		case "tickets", "ticket", "tix":
			field = "tix"
		}
		if field == "" {
			/*
				_, err := session.ChannelMessageSend(
					channel,
					"Specify correctly the kind of pulls you want to set.",
				)
			*/
			return "", 0, nil // err
		}
		if len(args) < 3 {
			// _, err := session.ChannelMessageSend(channel, "Specify how many pulls you want to set.")
			return "", 0, nil
		}
		quantity, err := strconv.Atoi(args[2])
		if err != nil {
			// _, err := session.ChannelMessageSend(channel, "Please input a number.")
			return "", 0, err
		}
		return field, quantity, nil
	}
}

func sparkUpdateHandler(session *dgo.Session, args []string, channel, discordId, op string) error {
	field, quantity, err := parseSparkArgs(session, args, channel)
	if err != nil {
		return err
	}
	var playerDataDict map[string]any
	collection := getDatabase().Collection("players")
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	result, err := collection.FindOne(ctx, bson.M{"discordId": discordId}).DecodeBytes()
	var totalBefore int64 = 0
	err = bson.Unmarshal(result, &playerDataDict)
	if err != nil {
		err = createPlayerDocument(session, channel, discordId, collection)
		if err != nil {
			return err
		}
	} else {
		totalBefore = getTotalPulls(playerDataDict)
	}
	if op == "set" {
		err = setQuantity(discordId, field, quantity, collection)
	} else {
		err = addQuantity(discordId, field, quantity, collection)
	}
	if err != nil {
		return err
	}
	result, _ = collection.FindOne(ctx, bson.M{"discordId": discordId}).DecodeBytes()
	_ = bson.Unmarshal(result, &playerDataDict)
	totalPulls := getTotalPulls(playerDataDict)
	message := fmt.Sprintf("You now have %d draws!", totalPulls)
	if totalBefore/300 < totalPulls/300 {
		message = message + "\n:confetti_ball: Congratulations! You've saved up a spark! :confetti_ball:"
	}
	// _, err = session.ChannelMessageSend(channel, message)
	return err
}

func showTime(session *dgo.Session, channel string) error {
	now := time.Now()
	location, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return err
	}
	tzTime := now.In(location)
	_ = tzTime.Format("Mon Jan _2 2006 15:04:05")
	/*
		_, err = session.ChannelMessageSend(
			channel,
			fmt.Sprintf("It is `%s` in Japan right now.", fmtTime),
		}
		if err != nil {
			return err
		}
	*/
	return nil
}

func getLastRoundsPerformance(crewId string) ([][]string, error) {
	resp, err := http.Get("https://gbfdata.com/en/guild/" + crewId)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	node, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var tableNode *html.Node
	tableNode = node.
		FirstChild.NextSibling.  // <html>
		FirstChild.              //   <head>
		NextSibling.NextSibling. //   <body>
		FirstChild.NextSibling.  //     <header>
		NextSibling.NextSibling. //     <div>
		FirstChild.NextSibling.  //       <div>
		FirstChild.NextSibling.  //         <div>
		NextSibling.NextSibling. //         <nav>
		NextSibling.NextSibling. //         <table>
		FirstChild.NextSibling.  //           <thead>
		NextSibling.NextSibling  //           <tbody>

	roundRow := tableNode.FirstChild.NextSibling
	lastRound := tableNode.FirstChild.NextSibling.FirstChild.NextSibling.FirstChild.Data
	rounds := make([][]string, 0)

	for {
		roundData := roundRow.FirstChild
		roundNumber := roundData.NextSibling
		if lastRound != roundNumber.FirstChild.Data {
			break
		}
		date := roundNumber.NextSibling.NextSibling.NextSibling.NextSibling
		rank := date.NextSibling.NextSibling.NextSibling.NextSibling
		dailyHonors := rank.NextSibling.NextSibling.NextSibling.NextSibling
		totalHonors := dailyHonors.NextSibling.NextSibling
		rounds = append(rounds, []string{date.FirstChild.Data, rank.FirstChild.Data, dailyHonors.FirstChild.Data, totalHonors.FirstChild.Data})
		roundRow = roundRow.NextSibling.NextSibling
	}

	return rounds, err
}

func getCrewGWMembers(crewId string) ([]userRankingData, error) {
	url := fmt.Sprintf("https://gbfdata.com/api/guilds/%s/members", crewId)

	resp, err := http.Get(url)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	jsonData := &crewAPIData{}
	err = json.Unmarshal(body, jsonData)

	if err != nil {
		return nil, err
	}

	return jsonData.MembersData, err
}

func getPlayersRanking(session *dgo.Session, channel, crewID string) error {
	players, err := getCrewGWMembers(crewID)
	if err != nil {
		return err
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].Ranking == nil {
			return false
		}
		if players[j].Ranking == nil {
			return true
		}
		return players[i].Ranking.Rank < players[j].Ranking.Rank
	})

	message := "```\n   Player\t\t Rank\t  GW rank     Total Honors\n"

	for n, player := range players {
		message += fmt.Sprint(n+1) + "."
		if n+1 < 10 {
			message += " "
		}
		message += player.Name + strings.Repeat(" ", 15-runewidth.StringWidth(player.Name))
		if runewidth.StringWidth(player.Name) < len(player.Name) {
			message += " "
		}
		if player.Ranking == nil {
			message += "No data.  No data.    No data."
		} else {
			message += fmt.Sprint(player.Ranking.Level) +
				strings.Repeat(" ", 9-int(math.Log10(float64(player.Ranking.Level)))) +
				fmt.Sprint(player.Ranking.Rank) +
				strings.Repeat(" ", 11-int(math.Log10(float64(player.Ranking.Rank)))) +
				fmt.Sprint(player.Ranking.Point)
		}
		message += "\n"
	}
	message += "```"

	embedMessage := dgo.MessageEmbed{Description: message, Title: "Wall of shame"}
	_, err = session.ChannelMessageSendEmbed(channel, &embedMessage)

	return err
}

func searchGWOpponent(session *dgo.Session, channel, opponent string) error {
	if opponent == "" {
		_, err := session.ChannelMessageSend(channel, "Please input a crew's name.")
		return err
	}

	values := map[string]string{"search": opponent}
	jsonData, err := json.Marshal(values)
	resp, err := http.Post(
		"http://gbf.gw.lt/gw-guild-searcher/search",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		_, _ = session.ChannelMessageSend(channel, "Sorry, something went wrong.")
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_, _ = session.ChannelMessageSend(channel, "Sorry, something went wrong.")
		return err
	}

	var data map[string]any
	err = json.Unmarshal(body, &data)
	result := data["result"].([]any)
	if len(result) == 0 {
		_, err = session.ChannelMessageSend(channel, "Crew not found.")
		return err
	}

	if len(result) > 1 {
		_, err = session.ChannelMessageSend(
			channel,
			fmt.Sprintf("Found %d crews with name `%s`", len(result), opponent),
		)
		if len(result) > 5 {
			_, err = session.ChannelMessageSend(channel, "Showing only the 5 most relevant ones")
		}
		// Delay so that messages don't come up too fast to read this.
		time.Sleep(time.Second)
	}

	for i, crew := range result {
		if i >= 5 {
			break
		}
		crewMap := crew.(map[string]any)
		crewData := crewMap["data"].([]any)
		crewId := fmt.Sprintf("%.f", crewMap["id"].(float64))

		message := "[__Crew's page__](http://game.granbluefantasy.jp/#guild/detail/" + crewId + ")\n```\n"
		for _, gwData := range crewData {
			unpackedData := gwData.(map[string]any)
			points := unpackedData["points"]
			if points == nil {
				continue
			}
			message = message + fmt.Sprintf(
				"%s - Ranked #%d in GW #%d with %s points\n",
				unpackedData["name"],
				int(unpackedData["rank"].(float64)),
				int(unpackedData["gw_num"].(float64)),
				intComma(int(points.(float64))),
			)
		}
		message = message + "```"
		embeddedMessage := dgo.MessageEmbed{
			Description: message,
		}

		_, err = session.ChannelMessageSendEmbed(channel, &embeddedMessage)

		if err != nil {
			session.ChannelMessageSend(channel, "Sorry, something went wrong when retrieving the data.")
			return err
		}

		rounds, err := getLastRoundsPerformance(crewId)

		if err != nil {
			session.ChannelMessageSend(channel, "Could not retrieve last rounds performance.")
			continue
		}

		message = "```\nDate\t\t\tRank\tDaily Honors      Total Honors\n"
		for _, round := range rounds {
			message += round[0] + "\t" + round[1] + strings.Repeat(" ", 8-len(round[1])) + round[2] + strings.Repeat(" ", 18-len(round[2])) + round[3] + "\n"
		}
		message += "```\n"

		embedMessage := dgo.MessageEmbed{Description: message, Title: "Crew's performance"}
		_, err = session.ChannelMessageSendEmbed(channel, &embedMessage)

		myRounds, err := getLastRoundsPerformance(myCrew)

		if err != nil {
			session.ChannelMessageSend(channel, "Could not retrieve last rounds performance for our crew.")
			continue
		}

		message = "```\nDate\t\t\tRank\tDaily Honors      Total Honors\n"
		for n, round := range myRounds {
			opponentRound := rounds[n]
			opponentRank, _ := strconv.ParseInt(opponentRound[1], 10, 64)
			opponentDaily, _ := strconv.ParseInt(strings.ReplaceAll(opponentRound[2], ",", ""), 10, 64)
			opponentTotal, _ := strconv.ParseInt(strings.ReplaceAll(opponentRound[3], ",", ""), 10, 64)
			ourRank, _ := strconv.ParseInt(round[1], 10, 64)
			ourDaily, _ := strconv.ParseInt(strings.ReplaceAll(round[2], ",", ""), 10, 64)
			ourTotal, _ := strconv.ParseInt(strings.ReplaceAll(round[3], ",", ""), 10, 64)
			rankDifference := ourRank - opponentRank
			rankDifferenceString := strconv.Itoa(int(rankDifference))
			if rankDifference >= 0 {
				rankDifferenceString = "+" + rankDifferenceString
			}
			dailyDifference := intComma(int(ourDaily - opponentDaily))
			if ourDaily-opponentDaily >= 0 {
				dailyDifference = "+" + dailyDifference
			}
			totalDifference := intComma(int(ourTotal - opponentTotal))
			if ourTotal-opponentTotal >= 0 {
				totalDifference = "+" + totalDifference
			}
			message += round[0] + "\t" + rankDifferenceString + strings.Repeat(" ", 8-len(rankDifferenceString)) + dailyDifference + strings.Repeat(" ", 18-len(dailyDifference)) + totalDifference + "\n"
		}
		message += "```\n"

		embedMessage = dgo.MessageEmbed{Description: message, Title: "Our crew vs " + opponent}
		_, err = session.ChannelMessageSendEmbed(channel, &embedMessage)

		time.Sleep(time.Second)
	}

	return nil
}

func bless(session *dgo.Session, channel string) error {
	var filename string
	if rand.Int()%2 == 1 {
		filename = "bless.png"
	} else {
		filename = "curse.png"
	}
	path := "img/" + filename
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = session.ChannelFileSend(channel, filename, f)
	return err
}

func translate(session *dgo.Session, channel, message string) error {
	logger.Println("Translating tweet in following message:\n" + message)
	urlRegex, err := regexp.Compile(`https://(?:www\.|mobile\.)?(?:twitter|x)\.com/\S+/status/\d+`)
	if err != nil {
		return err
	}
	urls := urlRegex.FindAllString(message, -1)
	logger.Printf("Found %d urls:\n", len(urls))
	logger.Printf("%v\n", urls)
	for _, URL := range urls {
		path, _ := launcher.LookPath()
		currentLauncher := launcher.New()
		defer currentLauncher.Kill()
		u := currentLauncher.Bin(path).Headless(true).MustLaunch()
		browser := rod.New().ControlURL(u).MustConnect()
		defer browser.MustClose()

		logger.Println("Browser connected. Opening page...")

		page, err := browser.Page(proto.TargetCreateTarget{URL: URL})
		if err != nil {
			return err
		}

		logger.Println("Page opened. Waiting for load...")

		//wait := page.MustWaitRequestIdle()
		page.MustWaitDOMStable()

		logger.Println("Load completed. Finding tweet elements...")

		spanElements := page.MustElements("span")

		if len(spanElements) < 2 {
			return fmt.Errorf("couldn't find the right elements in the tweet")
		}

		var tweetTextElement *rod.Element

		if ariaLabel, _ := spanElements[0].Attribute("aria-label"); ariaLabel != nil {
			if class, _ := spanElements[1].Attribute("class"); *class == "contents" {
				logger.Println("Empty tweet")
				return nil
			} else {

				tweetTextElement = spanElements[1]
			}
		} else {
			if class, _ := spanElements[0].Attribute("class"); *class == "contents" {
				logger.Println("Empty tweet")
				return nil
			} else {
				tweetTextElement = spanElements[0]
			}
		}

		logger.Println("Found tweet element. Obtaining text")

		tweetText, err := tweetTextElement.Text()
		if err != nil {
			return err
		}

		logger.Println("Text found: " + tweetText)

		toEraseRegex, err := regexp.Compile(`https://t\.co/[0-9a-zA-Z]+`)
		if err != nil {
			return err
		}

		logger.Println("Requesting translation...")

		tweetText = toEraseRegex.ReplaceAllString(tweetText, "")
		payload, _ := json.Marshal(map[string]any{"text": [1]string{tweetText}, "target_lang": "EN", "formality": "prefer_less"})
		request, _ := http.NewRequest("POST", "https://api-free.deepl.com/v2/translate", bytes.NewReader(payload))
		request.Header.Add("Content-Type", "application/json")
		request.Header.Add("Authorization", fmt.Sprintf("DeepL-Auth-Key %s", deeplKey))

		client := &http.Client{
			Timeout: time.Second * 10, // Timeout each requests
		}

		deeplResponse, err := client.Do(request)
		if err != nil {
			return err
		}
		defer deeplResponse.Body.Close()

		logger.Println("Response received. Parsing...")

		deeplResponseData := make(map[string][]map[string]string)

		// testData := make(map[string]string)
		// testBody, _ := io.ReadAll(deeplResponse.Body)
		// json.Unmarshal(testBody, &testData)
		// fmt.Println(testData)
		body, err := io.ReadAll(deeplResponse.Body)
		if err != nil {
			return err
		}
		err = json.Unmarshal(body, &deeplResponseData)
		if err != nil {
			return err
		}

		if deeplResponseData["translations"][0]["detected_source_language"] == "EN" {
			logger.Println("The tweet was in English, no need to post the translation")
			return nil
		}

		translatedTweet := html.UnescapeString(deeplResponseData["translations"][0]["text"])

		logger.Println("Translation obtained: " + translatedTweet)

		embeddedURLsRegex, err := regexp.Compile(`http(?:s)?:\/\/\S+`)
		if err != nil {
			return err
		}

		embeddedURLs := embeddedURLsRegex.FindAllString(translatedTweet, 10)

		for _, embeddedURL := range embeddedURLs {
			newURL := fmt.Sprintf("<%s>", strings.TrimSuffix(embeddedURL, "..."))
			translatedTweet = strings.Replace(translatedTweet, embeddedURL, newURL, 1)
		}

		_, err = session.ChannelMessageSend(channel, translatedTweet)
		if err != nil {
			return err
		}
	}
	return nil
}

func startHC(session *dgo.Session, channel string) error {
	// Run ngrok first
	if ngrokProcess != nil {
		session.ChannelMessageSend(channel, "The server is already up")
		return nil
	}
	cmd := exec.Command(ngrokPath, "tcp", "25565", "--region", "eu")
	err := cmd.Start()
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong with the server startup. Ping my creator.")
		return err
	}
	ngrokProcess = cmd.Process
	time.Sleep(2 * time.Second)
	resp, err := http.Get("http://localhost:4040/api/tunnels")
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong with the server startup. Ping my creator.")
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong with the server startup. Ping my creator.")
		return err
	}
	responseData := make(map[string]any)
	err = json.Unmarshal(body, &responseData)
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong with the server startup. Ping my creator.")
		return err
	}
	// Then run the MC server
	if mcCmd != nil {
		session.ChannelMessageSend(channel, "The server is already up")
		return nil
	}
	mcCmd = exec.Command("python3", "quick.py")
	mcCmd.Dir = mcDirPath
	mcCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// stdout, stdoutErr := mcCmd.StdoutPipe()
	// stderr, stderrErr := mcCmd.StderrPipe()
	err = mcCmd.Start()
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong with the server startup. Ping my creator.")
		ngrokProcess.Kill()
		return err
	}
	serverURL := responseData["tunnels"].([]any)[0].(map[string]any)["public_url"].(string)
	serverURL = strings.TrimPrefix(serverURL, "tcp://")
	mcURLMessage, err = session.ChannelMessageSend(channel, fmt.Sprintf("`%s`", serverURL))
	/*
		buff := make([]byte, 1000)
		for stdoutErr == nil {
			_, stdoutErr = stdout.Read(buff)
			fmt.Println(string(buff[:]))
		}
	*/
	return err
}

func stopHC(session *dgo.Session, channel string) error {
	if ngrokProcess == nil {
		session.ChannelMessageSend(channel, "There is no server running")
		return nil
	}
	message, _ := session.ChannelMessageSend(channel, "Stopping the server...")
	err := ngrokProcess.Kill()
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong stopping the server. Ping my creator.")
		return err
	}
	ngrokProcess.Wait()
	ngrokProcess = nil
	if mcCmd == nil {
		session.ChannelMessageSend(channel, "There is no server running")
		return nil
	}
	err = syscall.Kill(-mcCmd.Process.Pid, syscall.SIGINT)
	if err != nil {
		session.ChannelMessageSend(channel, "Something went wrong stopping the server. Ping my creator.")
		return err
	}
	mcCmd.Process.Wait()
	mcCmd = nil
	session.ChannelMessageDelete(mcURLMessage.ChannelID, mcURLMessage.ID)
	session.ChannelMessageEdit(message.ChannelID, message.ID, "Server stopped.")
	return err
}

func postSuiseiPic(session *dgo.Session, channel string) error {
	resp, err := http.Get("https://safebooru.org/index.php?page=dapi&s=post&q=index&tags=hoshimachi_suisei&limit=0&pid=0")
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	xmlString := string(body)
	re, _ := regexp.Compile(`.*count="(\d+)".*`)
	matches := re.FindStringSubmatch(xmlString)
	if len(matches) < 2 {
		return fmt.Errorf("the safebooru xml didn't contain any 'count'. Is this okay?")
	}
	posts := matches[1]
	postsInt, _ := strconv.ParseInt(posts, 10, 64)
	chosenPost := rand.Intn(int(postsInt))
	resp, err = http.Get(fmt.Sprintf("https://safebooru.org/index.php?page=dapi&s=post&q=index&tags=hoshimachi_suisei&limit=1&pid=%d", chosenPost))
	if err != nil {
		return err
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	xmlString = string(body)
	re, _ = regexp.Compile(`.*file_url="(.*)" parent_id.*`)
	matches = re.FindStringSubmatch(xmlString)
	if len(matches) < 2 {
		return fmt.Errorf("the safebooru xml didn't contain any 'count'. Is this okay?")
	}
	fileURL := matches[1]
	_, err = session.ChannelMessageSend(channel, fileURL)
	return err
}

func postSusPeko(session *dgo.Session, channel string) error {
	_, err := session.ChannelMessageSend(channel, "https://www.youtube.com/watch?v=f8qd_LwVUhc")
	return err
}

func postCunny(session *dgo.Session, channel string) error {
	_, err := session.ChannelMessageSend(channel, "https://youtu.be/3zIPp95GC3E")
	return err
}

func postHonse(session *dgo.Session, channel string, message *dgo.MessageCreate) error {
	_, err := session.ChannelMessageSendReply(channel, "https://jrryy.moe/honse.mp4", message.Reference())
	return err
}

func messageHandler(session *dgo.Session, m *dgo.MessageCreate) {
	allowed := strings.Contains(allowedChannels, m.ChannelID)
	if m.Author.ID == session.State.User.ID {
		return
	}
	message := strings.Trim(m.Content, " ")
	var e error
	if strings.Contains(message, "twitter.com") || strings.Contains(message, "x.com") && !strings.Contains(translationForbiddenChannels, m.ChannelID) {
		e = translate(session, m.ChannelID, message)
	}
	if strings.HasPrefix(message, "$suisex") {
		e = postSuiseiPic(session, m.ChannelID)
	}
	if strings.HasPrefix(message, "$suspeko") {
		e = postSusPeko(session, m.ChannelID)
	}
	if strings.HasPrefix(message, "$cunny") {
		e = postCunny(session, m.ChannelID)
	}
	if strings.Contains(strings.ToLower(message), "honse") {
		e = postHonse(session, m.ChannelID, m)
	}
	if allowed {
		if strings.HasPrefix(message, "$starthc") {
			e = startHC(session, m.ChannelID)
		}
		if strings.HasPrefix(message, "$stophc") {
			e = stopHC(session, m.ChannelID)
		}
		if strings.HasPrefix(message, "$help") {
			e = sendHelp(session, m.ChannelID)
		}
		if strings.HasPrefix(message, "$time") {
			e = showTime(session, m.ChannelID)
		}
		if strings.HasPrefix(message, "$bless") {
			e = bless(session, m.ChannelID)
		}
		if strings.HasPrefix(message, "$spark") {
			var args []string
			for arg := range strings.SplitSeq(strings.TrimPrefix(message, "$spark"), " ") {
				if arg != "" {
					args = append(args, arg)
				}
			}
			if args == nil {
				e = createOrRetrievePlayerData(session, m.ChannelID, m.Author.ID, m.Author.Username)
			} else {
				switch args[0] {
				case "h", "help":
					e = sendHelp(session, m.ChannelID)
				case "":
					e = createOrRetrievePlayerData(session, m.ChannelID, m.Author.ID, m.Author.Username)
				case "set", "add":
					e = sparkUpdateHandler(session, args, m.ChannelID, m.Author.ID, args[0])
				default:
					logger.Printf("The command '%s' was invalid ", m.Content)
				}
			}
		}
		if after, ok := strings.CutPrefix(message, "$gw"); ok {
			opponent := strings.Trim(after, " ")
			e = searchGWOpponent(session, m.ChannelID, opponent)
		}
		if strings.HasPrefix(message, "$shame") {
			e = getPlayersRanking(session, m.ChannelID, myCrew)
		}
	}
	if e != nil {
		fmt.Println(e)
		fmt.Println("Error triggered by message:")
		fmt.Println(m.Content)
	}
}

func getToken(tokenVariable *string, envVariable string) (e error) {
	value, found := syscall.Getenv(envVariable)
	if !found {
		e = fmt.Errorf("%s not set", envVariable)
	} else {
		*tokenVariable = value
	}
	return
}

func main() {
	envVariables := []string{
		"NIETE_TOKEN",
		"NIETE_CHANNELS",
		"TRANSLATION_FORBIDDEN_CHANNELS",
		"DEEPL_KEY",
		"NGROK_PATH",
		"MC_DIR_PATH",
	}
	variables := []*string{
		&discordToken,
		&allowedChannels,
		&translationForbiddenChannels,
		&deeplKey,
		&ngrokPath,
		&mcDirPath,
	}
	for i := range envVariables {
		e := getToken(variables[i], envVariables[i])
		if e != nil {
			fmt.Println(e)
			return
		}
	}
	e := getToken(&myCrew, "MY_CREW")
	if e != nil {
		myCrew = ""
	}
	session, e := dgo.New("Bot " + discordToken)
	if e != nil {
		fmt.Println("An error occurred when opening a connection to Discord: ", e)
		return
	}
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	mongoClient, e = mongo.Connect(ctx, options.Client().ApplyURI("mongodb://db:27017"))
	if e != nil {
		fmt.Println("An error occurred when connecting to mongodb: ", e)
		return
	}

	// Register the messageCreate func as a callback for MessageCreate events.
	session.AddHandler(messageHandler)

	// Open a websocket connection to Discord and begin listening.
	e = session.Open()
	if e != nil {
		fmt.Println("Error opening connection, ", e)
		return
	}

	logFile, e := os.OpenFile("niete.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, os.ModePerm)
	if e != nil {
		fmt.Println("Error creating log file, ", e)
		return
	}

	logger = *log.Default()
	logger.SetOutput(logFile)
	defer logFile.Close()

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanly close down the Discord session.
	e = session.Close()
	if e != nil {
		fmt.Println("An error occurred when closing the Discord session: ", e)
	}
}
