package main

import (
	"flag"
	"fmt"
	pb "github.com/cheggaaa/pb/v3"
	"github.com/domainr/whois"
	"github.com/evilsocket/islazy/async"
	"github.com/evilsocket/islazy/tui"
	tld "github.com/jpillora/go-tld"
	"github.com/likexian/whois-parser-go"
	"golang.org/x/net/idna"
	"net"
	"os"
	"encoding/csv"
	"strings"
)

type Entry struct {
	Domain    string
	Ascii     string
	Available bool
	Whois     *whoisparser.WhoisInfo
	Addresses []string
	Names     []string
}

var (
	url         = "https://www.ice.gov"
	limit       = 0
	entries     = make([]*Entry, 0)
	queue       = async.NewQueue(0, processEntry)
	progress    = (* pb.ProgressBar)(nil)
	availOnly   = false
	regOnly     = false
	liveOnly    = false
	whoisInfo   = false
	csvFileName = ""
)

func die(format string, a ...interface{}) {
	fmt.Printf(format, a...)
	os.Exit(1)
}

func init() {
	flag.StringVar(&url, "domain", url, "Domain name or url.")
	flag.IntVar(&limit, "limit", limit, "Limit the number of permutations.")
	flag.BoolVar(&availOnly, "available", availOnly, "Only display available domain names.")
	flag.BoolVar(&regOnly, "registered", regOnly, "Only display registered domain names.")
	flag.BoolVar(&liveOnly, "live", liveOnly, "Only display registered domain names that also resolve to an IP.")
	flag.BoolVar(&whoisInfo, "whois", whoisInfo, "Show whois information.")
	flag.StringVar(&csvFileName, "csv", csvFileName, "If set ditto will save results to this CSV file.")
}

func genEntries(parsed *tld.URL) {
	for i, c := range parsed.Domain {
		if substitutes, found := dictionary[c]; found {
			for _, sub := range substitutes {
				entries = append(entries, &Entry{
					Domain: fmt.Sprintf("%s%s%s.%s", parsed.Domain[:i], sub, parsed.Domain[i+1:], parsed.TLD),
				})
				if limit > 0 && len(entries) == limit {
					return
				}
			}
		}
	}
}

func isAvailable(domain string) (bool, *whoisparser.WhoisInfo) {
	req, err := whois.NewRequest(domain)
	if err != nil {
		return true, nil
	}

	resp, err := whois.DefaultClient.Fetch(req)
	if err != nil {
		return true, nil
	}

	parsed, err := whoisparser.Parse(string(resp.Body))
	if err != nil {
		return true, nil
	}

	return false, &parsed
}

func processEntry(arg async.Job) {
	defer progress.Increment()

	entry := arg.(*Entry)
	entry.Available, entry.Whois = isAvailable(entry.Domain)
	entry.Ascii, _ = idna.ToASCII(entry.Domain)
	// some whois might only be accepting ascii encoded domain names
	if entry.Available {
		entry.Available, entry.Whois = isAvailable(entry.Ascii)
	}

	if !entry.Available {
		entry.Addresses, _ = net.LookupHost(entry.Ascii)
		uniq := make(map[string]bool)
		for _, addr := range entry.Addresses {
			names, _ := net.LookupAddr(addr)
			for _, name := range names {
				uniq[name] = true
			}
		}
		for name, _ := range uniq {
			entry.Names = append(entry.Names, name)
		}
	}
}

func printEntry(entry *Entry) {
	if entry.Available {
		if !regOnly && !liveOnly {
			fmt.Printf("%s (%s) : %s\n", entry.Domain, entry.Ascii, tui.Green("available"))
		}
	} else {
		if !availOnly {
			mainFields := []string{}
			whoisFields := []string{}
			isLive := len(entry.Addresses) > 0

			if isLive {
				mainFields = append(mainFields, fmt.Sprintf("ips=%s", strings.Join(entry.Addresses, ",")))
				if len(entry.Names) > 0 {
					mainFields = append(mainFields, fmt.Sprintf("names=%s", strings.Join(entry.Names, ",")))
				}
			}

			if entry.Whois != nil {
				if entry.Whois.Registrar != nil {
					whoisFields = append(whoisFields, fmt.Sprintf("registrar=%s", entry.Whois.Registrar.ReferralURL))
				}

				if entry.Whois.Domain != nil {
					whoisFields = append(whoisFields, fmt.Sprintf("created=%s", entry.Whois.Domain.CreatedDate))
					whoisFields = append(whoisFields, fmt.Sprintf("updated=%s", entry.Whois.Domain.UpdatedDate))
					whoisFields = append(whoisFields, fmt.Sprintf("expires=%s", entry.Whois.Domain.ExpirationDate))
					whoisFields = append(whoisFields, fmt.Sprintf("ns=%s", strings.Join(entry.Whois.Domain.NameServers, ",")))
				}
			}

			if isLive || !liveOnly {
				fmt.Printf("%s (%s) %s",
					entry.Domain,
					entry.Ascii,
					tui.Red("registered"))

				if len(mainFields) > 0 {
					fmt.Printf(" : %s", strings.Join(mainFields, " "))
				}

				fmt.Println()

				if whoisInfo && len(whoisFields) > 0 {
					for _, field := range whoisFields {
						fmt.Printf("  %s\n", field)
					}
				}
			}
		}
	}
}

func main() {
	flag.Parse()

	// the tld library requires the schema or it won't parse the domain ¯\_(ツ)_/¯
	if !strings.Contains(url, "://") {
		url = fmt.Sprintf("https://%s", url)
	}

	parsed, err := tld.Parse(url)
	if err != nil {
		die("%v\n", err)
	} else if parsed.Domain == "" {
		die("could not parse %s\n", url)
	}

	genEntries(parsed)

	fmt.Printf("checking %d variations for '%s.%s', please wait ...\n\n", len(entries), parsed.Domain, parsed.TLD)

	progress = pb.StartNew(len(entries))

	for _, entry := range entries {
		queue.Add(async.Job(entry))
	}

	queue.WaitDone()

	progress.Finish()

	fmt.Printf("\n\n")

	for _, entry := range entries {
		printEntry(entry)
	}

	if csvFileName != "" {
		fmt.Printf("\n\n")

		file, err := os.Create(csvFileName)
		if err != nil {
			die("error creating %s: %v\n", csvFileName, err)
		}
		defer file.Close()

		writer := csv.NewWriter(file)
		defer writer.Flush()

		columns := []string {
			"unicode",
			"ascii",
			"status",
			"ips",
			"names",
		}

		if whoisInfo {
			columns = append(columns, []string{
				"registrar",
				"created_at",
				"updated_at",
				"expires_at",
				"nameservers",
			}...)
		}

		if err = writer.Write(columns); err != nil {
			die("error writing header: %v\n", err)
		}

		for _, entry := range entries {
			row := []string{
				entry.Domain,
				entry.Ascii,
			}

			if entry.Available {
				row = append(row, "available")
			} else {
				row = append(row, "registered")
			}

			row = append(row, strings.Join(entry.Addresses, ","))
			row = append(row, strings.Join(entry.Names, ","))

			if whoisInfo {
				if entry.Whois != nil {
					if entry.Whois.Registrar != nil {
						row = append(row, entry.Whois.Registrar.ReferralURL)
					} else {
						row = append(row, "")
					}

					if entry.Whois.Domain != nil {
						row = append(row, entry.Whois.Domain.CreatedDate)
						row = append(row, entry.Whois.Domain.UpdatedDate)
						row = append(row, entry.Whois.Domain.ExpirationDate)
						row = append(row, strings.Join(entry.Whois.Domain.NameServers, ","))
					} else {
						row = append(row, []string{
							"", "", "", ""}...)
					}
				} else {
					row = append(row, []string{
						"", "", "", "", ""}...)
				}
			}

			if err = writer.Write(row); err != nil {
				die("error writing line: %v\n", err)
			}
		}

		fmt.Printf("saved to %s\n", csvFileName)
	}
}
