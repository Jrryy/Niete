# Niete

A self hosted discord bot that emulates [Europa](https://cryotalis.github.io/Europa/), since it didn't seem to retrieve messages in certain servers.
It emulates some of its features and adds others.

Made with go and mongodb.

To run, execute `docker-compose up`. Requires an `env_vars.env` file with:
- `NIETE_TOKEN`: The bot's Token in your Discord account's developers platform.
- `NIETE_CHANNELS`: A comma separated list of IDs of the channels in which the bot will interact.

### Features

- `$time`: Displays the current date and time in Japan (JST).
```
> $time
It is Thu Apr 22 2021 05:43:14 in Japan right now.
```

- `$spark`: Shows your spark data. Creates a profile if your user is not in the database yet.
```
> $spark
Profile not found. Creating...

> $spark
Your name
Crystals: 0
Tickets: 0
10 part tickets: 0
Total pulls saved: 0
[                                                                                                    ] 0.00%
```

- `$spark set [xtals|crystals|tix|ticket|tickets|10part] <int>`: sets a new amount of the specified item.
```
> $spark set tix 1
You now have 1 draws!

> $spark set 10part 1
You now have 11 draws!

> $spark set xtals 141449
You now have 482 draws!
:confetti_ball: Congratulations! You've saved up 1 spark! :confetti_ball:

> $spark
Your name
Crystals: 141449
Tickets: 1
10 part tickets: 1
Total pulls saved: 482
[████████████████████████████████████████████████████████████▊                                       ] 160.67%
```

- `$gw <string>`: Displays the list of past performances in GW of the specified crew.

```
> $gw abc
```
> [Crew's page](http://game.granbluefantasy.jp/#guild/detail/785530)
> ```
> abc - Ranked #19735 in GW #56 with 186,074,223 points
> abc - Ranked #18328 in GW #55 with 166,782,588 points
> abc - Ranked #14197 in GW #54 with 173,442,586 points
> ...
> ```

- `$help`: Displays a help message explaining these commands.