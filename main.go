package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nlopes/slack"

	"github.com/shomali11/slacker"

	"github.com/kelseyhightower/envconfig"
)

// Version is the candebot version. Usually the git commit hash. Passed during building.
var Version = "unknown"

const (
	msgCOC        = "Please find our Code Of Conduct here: https://bcneng.github.io/coc/"
	msgNetiquette = "Please find our Netiquette here: https://bcneng.github.io/netiquette/"
)

const (
	sdecandelarioBirthday = "17/09/2019"
)

const (
	hiringJobBoardChannelID                        = "C30CUFT2B"
	hiringJobBoardWrongFormatNotificationChannelID = "G983W7L9F"
	candebotTestingChannelID                       = "CK32YCX5M"
)

var staff = []string{
	"U2Y6QQHST", //<@gonzaloserrano>
	"U2WPLA0KA", //<@smoya>
	"U3256HZH9", //<@mavi>
	"U36H6F3CN", //<@sdecandelario>
	"U7PQZMZ4L", //<@koe>
}

// Cache and optimizations
var (
	staffMap             map[string]struct{}
	channelNameToIDCache map[string]string
)

type specification struct {
	Port         int    `default:"8080"`
	BotUserToken string `required:"true" split_words:"true"`
	UserToken    string `required:"true" split_words:"true"`
	Debug        bool
}

func main() {
	var s specification
	err := envconfig.Process("candebot", &s)
	if err != nil {
		log.Fatal(err.Error())
	}

	adminClient := slack.New(s.UserToken)
	bot := slacker.NewClient(s.BotUserToken, slacker.WithDebug(s.Debug))
	bot.EventHandler(eventHandler(bot.Client(), adminClient))

	registerCommands(bot)
	go registerSlashCommands(s)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = bot.Listen(ctx)
	if err != nil {
		log.Fatal(err)
	}
}

func eventHandler(c, adminClient *slack.Client) slacker.EventHandler {
	return func(ctx context.Context, s *slacker.Slacker, msg slack.RTMEvent) error {
		switch event := msg.Data.(type) {
		case *slack.MessageEvent:
			if len(event.User) == 0 || len(event.BotID) > 0 {
				break
			}

			if event.Channel == hiringJobBoardChannelID || event.Channel == candebotTestingChannelID {
				if event.SubType != "" || event.ThreadTimestamp != "" {
					// We only want messages posted by humans. We also skip join/leave channel messages, etc by doing this.
					// Thread messages are also skipped.
					break
				}

				if event.Channel == candebotTestingChannelID {
					// Playground here

					return nil
				}

				r, _ := regexp.Compile(`(?mi)([^-]{1,})\@([^-]{1,})\-([^-]{1,})\-([^-]{1,})\-([^-]{1,})(\-[^-]{1,}){0,}`)
				matched := r.MatchString(event.Text)
				if !matched {
					link, err := c.GetPermalink(&slack.PermalinkParameters{
						Channel: event.Channel,
						Ts:      event.Timestamp,
					})
					if err != nil {
						log.Printf("error fetching permalink for channel %s and ts %s\n", hiringJobBoardWrongFormatNotificationChannelID, event.Timestamp)
					}

					_ = send(
						c,
						hiringJobBoardWrongFormatNotificationChannelID,
						fmt.Sprintf("new Job post with invalid format: %s", link),
						true,
					)
				}
			}
		}
		return slacker.DefaultEventHandler(ctx, s, msg)
	}
}

func registerSlashCommands(s specification) {
	http.HandleFunc("/slash", func(w http.ResponseWriter, r *http.Request) {
		s, err := slack.SlashCommandParse(r)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// TODO verify request

		switch s.Command {
		case "/coc":
			msg := &slack.Msg{Text: msgCOC}
			writeSlashResponse(w, msg)
		case "/netiquette":
			msg := &slack.Msg{Text: msgNetiquette}
			writeSlashResponse(w, msg)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	})
	log.Println("[INFO] Slash server listening on port", s.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", s.Port), nil))
}

func registerCommands(bot *slacker.Slacker) {
	bot.DefaultCommand(func(request slacker.Request, response slacker.ResponseWriter) {
		msg := "Say what?, try typing `help` to see all the things I can do for you ;)"
		_ = sendEphemeral(bot.Client(), request.Event().Channel, request.Event().User, msg)
	})

	bot.Command("coc", &slacker.CommandDefinition{
		Description: "Link to the Code Of Conduct of BcnEng",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(msgCOC)
		},
	})

	bot.Command("netiquette", &slacker.CommandDefinition{
		Description: "Link to the netiquette of BcnEng",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply(msgNetiquette)
		},
	})

	dob, _ := time.Parse("2/1/2006", sdecandelarioBirthday) // nolint: errcheck
	bot.Command("candebirthday", &slacker.CommandDefinition{
		Description: "Days until @sdecandelario birthday!",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			d := calculateTimeUntilBirthday(dob)

			var msg string
			if d.Hours() == 0 {
				msg = ":birthdaypartyparrot: :party: :birthday: HAPPY BIRTHDAY <@sdecandelario>! :birthday: :party: :birthdaypartyparrot:"
			} else {
				msg = fmt.Sprintf(":birthday: %d days until <@sdecandelario> birthday! :birthday:", int(d.Hours()/24))
			}

			response.Reply(msg)
		},
	})

	bot.Command("staff", &slacker.CommandDefinition{
		Description: "Info about the staff behind BcnEng",
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			// Shuffle the order of members list
			shuffledMembers := staff
			rand.Shuffle(len(shuffledMembers), func(i, j int) {
				shuffledMembers[i], shuffledMembers[j] = shuffledMembers[j], shuffledMembers[i]
			})

			members := strings.Join(shuffledMembers, ">\n• <@")
			m := fmt.Sprintf("Here is the list of the current staff members: \n\n• <@%s>", members)

			response.Reply(m)
		},
	})

	bot.Command("echo <channel> <message>", &slacker.CommandDefinition{
		Description: "Sends a message as Candebot",
		Example:     "echo #general Hi folks!",
		AuthorizationFunc: func(request slacker.Request) bool {
			return isStaff(request.Event().User)
		},
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			channel := strings.TrimPrefix(request.Param("channel"), "#")
			msg := request.Param("message")

			if channel == "" || msg == "" {
				_ = sendEphemeral(bot.Client(), request.Event().Channel, request.Event().User, "Channel and message are required.")
				return
			}

			// Fixes the lack of support of multi word params.
			if i := strings.Index(channel, " "); i > 0 {
				msg = channel[i:] + " " + msg
				channel = channel[0:i]
			}

			channelID, err := findChannelIDByName(bot.Client(), channel)
			if err != nil {
				log.Println(err.Error())
				_ = sendEphemeral(bot.Client(), request.Event().Channel, request.Event().User, "Internal error. Try again.")
				return
			}

			err = send(bot.Client(), channelID, msg, false)
			if err != nil {
				log.Println(err.Error())
				_ = sendEphemeral(bot.Client(), request.Event().Channel, request.Event().User, "Internal error. Try again.")
				return
			}
		},
	})

	bot.Command("version", &slacker.CommandDefinition{
		Handler: func(request slacker.Request, response slacker.ResponseWriter) {
			response.Reply("`" + Version + "`")
		},
	})
}

func findChannelIDByName(client *slack.Client, channel string) (string, error) {
	if channelNameToIDCache == nil {
		channelNameToIDCache = make(map[string]string)
	}

	id, ok := channelNameToIDCache[channel]
	if ok {
		return id, nil
	}

	chans, err := client.GetChannels(true, slack.GetChannelsOptionExcludeMembers())
	if err != nil {
		return "", err
	}

	for _, c := range chans {
		if c.Name == channel {
			return c.ID, nil
		}
	}

	privateChans, err := client.GetGroups(true)
	if err != nil {
		return "", err
	}

	for _, c := range privateChans {
		if c.Name == channel {
			channelNameToIDCache[channel] = c.ID // It is fine to not lock.

			return c.ID, nil
		}
	}

	return "", fmt.Errorf("channel %s not found", channel)
}

func sendEphemeral(c *slack.Client, channelID, userID, msg string) error {
	_, err := c.PostEphemeral(channelID, userID, slack.MsgOptionText(msg, true), slack.MsgOptionAsUser(true))
	if err != nil {
		log.Println("error sending ephemeral msg in channel ", channelID)
	}

	return err
}

func send(c *slack.Client, channelID, msg string, scape bool) error {
	_, _, err := c.PostMessage(channelID, slack.MsgOptionText(msg, scape), slack.MsgOptionAsUser(true))
	if err != nil {
		log.Println("error sending msg in channel ", channelID)
	}

	return err
}

func writeSlashResponse(w http.ResponseWriter, msg *slack.Msg) {
	b, err := json.Marshal(msg)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func calculateTimeUntilBirthday(t time.Time) time.Duration {
	n := time.Now()
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
	birthday := time.Date(today.Year(), t.Month(), t.Day(), 0, 0, 0, 0, n.Location())

	if birthday.Before(today) {
		// birthday next year!
		birthday = birthday.AddDate(1, 0, 0)
	}

	return birthday.Sub(today)
}

func isStaff(userID string) bool {
	if staffMap == nil {
		staffMap = make(map[string]struct{}, len(staff)) // It is fine to not lock.
		for _, u := range staff {
			staffMap[u] = struct{}{}
		}

	}

	_, ok := staffMap[userID]

	return ok
}
