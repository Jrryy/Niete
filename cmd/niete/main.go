package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	dgo "github.com/bwmarrin/discordgo"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"html"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var discordToken, allowedChannels, translateUser, twitterToken, deeplKey string
var mongoClient *mongo.Client

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

func getXtalsTixAnd10Parts(data map[string]interface{}) (int64, int64, int64) {
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

func sendPlayerData(session *dgo.Session, channel string, data map[string]interface{}) (e error) {
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
	var playerDataDict map[string]interface{}
	collection := getDatabase().Collection("players")
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	result, err := collection.FindOne(ctx, bson.M{"discordId": discordId}).DecodeBytes()
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
	var playerDataDict map[string]interface{}
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

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		_, _ = session.ChannelMessageSend(channel, "Sorry, something went wrong.")
		return err
	}

	var data map[string]interface{}
	err = json.Unmarshal(body, &data)
	result := data["result"].([]interface{})
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
		crewMap := crew.(map[string]interface{})
		crewData := crewMap["data"].([]interface{})
		crewId := fmt.Sprintf("%.f", crewMap["id"].(float64))

		message := "[__Crew's page__](http://game.granbluefantasy.jp/#guild/detail/" + crewId + ")\n```\n"
		for _, gwData := range crewData {
			unpackedData := gwData.(map[string]interface{})
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
		time.Sleep(time.Second)
	}
	return err
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
	urlRegex, err := regexp.Compile(".*https://(?:www\\.)?twitter\\.com/\\S+/status/(\\d+).*")
	if err != nil {
		return err
	}
	id := urlRegex.FindStringSubmatch(message)[1]
	twitterURL := fmt.Sprintf("https://api.twitter.com/2/tweets/%s?tweet.fields=lang", id)
	client := http.Client{}
	request, err := http.NewRequest(http.MethodGet, twitterURL, nil)
	if err != nil {
		return err
	}
	request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", twitterToken))
	twitterResponse, err := client.Do(request)
	if err != nil {
		return err
	}
	defer twitterResponse.Body.Close()

	body, err := ioutil.ReadAll(twitterResponse.Body)
	if err != nil {
		return err
	}
	tweetResponseData := make(map[string]map[string]string)
	err = json.Unmarshal(body, &tweetResponseData)
	if err != nil {
		return err
	}
	if tweetResponseData["data"]["lang"] == "ja" {
		tweetText := tweetResponseData["data"]["text"]
		toEraseRegex, err := regexp.Compile("https://t\\.co/[0-9a-zA-Z]+")
		if err != nil {
			return err
		}
		tweetText = url.QueryEscape(toEraseRegex.ReplaceAllString(tweetText, ""))
		deeplResponse, err := http.Post(
			"https://api-free.deepl.com/v2/translate",
			"application/x-www-form-urlencoded",
			strings.NewReader(fmt.Sprintf(
				"auth_key=%s&source_lang=JA&target_lang=EN&text=%s",
				deeplKey,
				tweetText,
			)),
		)
		if err != nil {
			return err
		}
		defer deeplResponse.Body.Close()
		deeplResponseData := make(map[string][]map[string]string)
		body, err := ioutil.ReadAll(deeplResponse.Body)
		if err != nil {
			return err
		}
		err = json.Unmarshal(body, &deeplResponseData)
		if err != nil {
			return err
		}
		_, err = session.ChannelMessageSend(channel, html.UnescapeString(deeplResponseData["translations"][0]["text"]))
	}
	return err
}

func messageHandler(session *dgo.Session, m *dgo.MessageCreate) {
	allowed := strings.Contains(allowedChannels, m.ChannelID)
	if m.Author.ID == session.State.User.ID {
		return
	}
	message := strings.Trim(m.Content, " ")
	var e error
	if strings.Contains(message, "twitter.com") && m.Author.ID == translateUser {
		e = translate(session, m.ChannelID, message)
	}
	if allowed {
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
			for _, arg := range strings.Split(strings.TrimPrefix(message, "$spark"), " ") {
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
					log.Printf("The command '%s' was invalid ", m.Content)
				}
			}
		}
		if strings.HasPrefix(message, "$gw") {
			opponent := strings.Trim(strings.TrimPrefix(message, "$gw"), " ")
			e = searchGWOpponent(session, m.ChannelID, opponent)
		}
	}
	if e != nil {
		log.Print(e)
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
		"NIETE_TRANSLATE",
		"TWITTER_TOKEN",
		"DEEPL_KEY",
	}
	variables := []*string{
		&discordToken,
		&allowedChannels,
		&translateUser,
		&twitterToken,
		&deeplKey,
	}
	for i := range envVariables {
		e := getToken(variables[i], envVariables[i])
		if e != nil {
			fmt.Println(e)
			return
		}
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
		fmt.Println("error opening connection, ", e)
		return
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	e = session.Close()
	if e != nil {
		fmt.Println("An error occurred when closing the Discord session: ", e)
	}
}
