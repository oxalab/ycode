package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"ycode/packages/llm/src/utils/oauth"
)

const AuthFileName = "auth.json"

// AuthEntry: Represents a stored OAuth credential
type AuthEntry struct {
	Type   string 	`json:"type"`
	oauth.OAuthCredentials
}

// LoadAuth: Loads OAuth credentials from auth.json
func LoadAuth() (map[string]AuthEntry, error) {
	data, err := os.ReadFile(AuthFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]AuthEntry), nil
		}
		return nil, err
	}
	var auth map[string]AuthEntry
	if err := json.Unmarshal(data, &auth); err != nil {
		return make(map[string]AuthEntry), nil
	}
	if auth == nil {
		return make(map[string]AuthEntry), nil
	}
	return auth, nil
}

// SaveAuth: Saves OAuth credentials to auth.json
func SaveAuth(auth map[string]AuthEntry) error {
	data, err := json.MarshalIndent(auth, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(AuthFileName, data, 0600)
}

// Prompt: Prompts user for input
func Prompt(question string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(question + " ")
	return reader.ReadString('\n')
}

// ListOAuthProviders: Lists all available OAuth providers
func ListOAuthProviders() {
	fmt.Println("Available OAuth providers:\n ")
	providers := oauth.GetOAuthProviders()
	for _, p := range providers {
		fmt.Printf(" %-20s %s\n", p.ID(), p.Name())
	}
}

// RunOAuthLogin: Runs the OAuth login flow for a provider
func RunOAuthLogin(providerID string) error {
	provider, ok := oauth.GetOAuthProvider(providerID)
	if !ok {
		return fmt.Errorf("Unknown provider: %s", provider)
	}
	fmt.Printf("Logging in to %s...\n", providerID)
	creds, err := provider.Login(context.TODO(), oauth.OAuthLoginCallbacks{
		OnAuth : func(info oauth.OAuthAuthInfo) {
			fmt.Printf("\nOpen this URL in your browser:\n%s\n", info.URL)
			if info.Instructions != "" {
				fmt.Println(info.Instructions)
			}
			fmt.Println()
		},
		OnPrompt: func(prompt oauth.OAuthPrompt) (string, error) {
			input, err := Prompt(prompt.Message)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(input), nil
		},
		OnProgress: func(message string) {
			fmt.Println(message)
		},
	})
	if err != nil {
		return fmt.Errorf("Login failed: %w", err)
	}
	auth, err := LoadAuth()
	if err != nil {
		return fmt.Errorf("failed to load auth file: %w", err)
	}
	auth[providerID] = AuthEntry {
		Type: "oauth",
		OAuthCredentials: creds,
	}
	if err := SaveAuth(auth); err != nil {
		return fmt.Errorf("failed to save auth file: %w", err)
	}
	fmt.Printf("\nCredentials saved to %s\n", AuthFileName)
	return nil
}

// Select Provider prompts the user to select a provider interactively
func SelectProvider() (string, error) {
	providers := oauth.GetOAuthProviders()
	if len(providers) == 0 {
		return "", fmt.Errorf("no OAuth providers are available")
	}
	fmt.Println("Select a provider: \n ")
	for i, p := range providers {
		fmt.Printf("  %d. %s\n", i+1, p.Name())
	}
	fmt.Println()
	input, err := Prompt(fmt.Sprintf("Enter number (1-%d):", len(providers)))
	if err != nil {
		return "", err
	}
	var choice int
	_, err = fmt.Sscanf(input, "%d", &choice)
	if err != nil || choice < 1 || choice > len(providers) {
		return "", fmt.Errorf("invalid selection")
	}
	return providers[choice-1].ID(), nil
}

// PrintHelp: Prints the CLI help message
func PrintHelp() {
	fmt.Printf(`Usage: llm-oauth <command> [provider]

Commands:
	login [provider] 	Login to an OAuth provider
	list 				List available providers

Providers:
`)
	ListOAuthProviders()
	fmt.Printf(`
Examples:
	llm-oauth login 			# interactive provider selection
	llm-oauth login anthropic	# login to specific provider
	llm-oauth list 				# list providers
`)
}

// RunCLI: Main entry for the CLI tool
func RunCLI(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
        PrintHelp()
        return nil
    }
	command := args[0]
	switch command{
	case "list":
		ListOAuthProviders()
		return nil
	case "login":
		providerID := ""
		if len(args) > 1{
			providerID = args[1]
		} else {
			var err error
			providerID, err = SelectProvider()
			if err != nil {
				return err
			}
		}
		if _, ok := oauth.GetOAuthProvider(providerID); !ok {
			return fmt.Errorf("Unknown provider: %s", providerID)
		}
		return RunOAuthLogin(providerID)
	default:
		return fmt.Errorf("Unknown command: %s\nUse '--help' for usage", command)
	}
}

func main() {
	if err := RunCLI(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n ", err)
		os.Exit(1)
	}
}