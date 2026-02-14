package main

import (
	"bufio"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

// CONFIGURATION
var (
	SenderEmail    string
	SenderPassword string
)

const (
	SMTPServer = "smtp.gmail.com"
	SMTPPort   = "587"
)

// The HTML Template
// UPDATED: Focused on "Human-in-the-Loop" workflow to appease CPA fears
const htmlBody = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color: #333; line-height: 1.6; padding: 10px; }
        .highlight { background-color: #e3f2fd; padding: 0 4px; font-weight: bold; color: #0d47a1; }
        ul { margin-bottom: 15px; padding-left: 20px; }
        li { margin-bottom: 8px; }
        .signature { margin-top: 20px; border-top: 1px solid #eee; padding-top: 10px; }
    </style>
</head>
<body>
    <p>Hi,</p>
    <p>I saw you're hiring a Junior Accountant. Usually, that means you're drowning in receipt chasing, manual data entry, and overdue invoices.</p>
    
    <p><strong>I don't want the job.</strong> I run an AI automation startup that handles these specific problems.</p>
    
    <p>I know the biggest fear with AI is a "black box" messing up your ledger. That is why I built a <strong>Human-in-the-Loop</strong> workflow. I treat AI as a "drafter," not a "poster."</p>

    <p>Instead of a junior (who takes 3 months to ramp up), here is how my "Safe Mode" works immediately:</p>
    <ul>
        <li><strong>1. AI Drafts the Entry:</strong> My agents read receipts/bills and categorize them based on your historical data.</li>
        <li><strong>2. The "Review Queue":</strong> Nothing is posted to QuickBooks yet. The AI places the transaction in a pending queue.</li>
        <li><strong>3. Human Approval:</strong> I review the queue. If it looks good, I click "Approve." If not, I correct it once, and the AI learns.</li>
    </ul>

    <p>You get the speed of AI, but the <strong>control remains 100% with me.</strong></p>

    <p><strong>Cost:</strong> <span class="highlight">Flat retainer.</span> (Half the cost of a hire and no benefits).</p>
    
    <p>I'm looking for one more partner firm to onboard this week. Do you have 15 minutes to see the "Review Queue" in action?</p>
    
    <div class="signature">
        Best,<br>
        Yankz.<br>
        <span style="font-size: 0.9em; color: #666;">Founder, usetoro.io</span>
    </div>
</body>
</html>
`

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return // Ignore if file not found
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

func loadConfig() {
	// Try to load from container/.env if running locally or in specific structure
	loadDotEnv("container/.env")
	// Also try local .env for convenience if container folder doesn't exist
	loadDotEnv(".env")

	SenderEmail = os.Getenv("EMAIL_HOST_USER")
	SenderPassword = os.Getenv("EMAIL_HOST_PASSWORD")
}

func main() {
	loadConfig()
	// Validate configuration
	if SenderEmail == "" || SenderPassword == "" {
		fmt.Println("Error: EMAIL_HOST_USER and EMAIL_HOST_PASSWORD environment variables must be set.")
		fmt.Println("Please ensure they are defined in your environment or .env file")
		os.Exit(1)
	}

	// Authentication setup
	auth := smtp.PlainAuth("", SenderEmail, SenderPassword, SMTPServer)
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("-------------------------------------------------")
	fmt.Println("   LEAD GEN EMAIL BLASTER (GMAIL SMTP)")
	fmt.Println("-------------------------------------------------")
	fmt.Printf("Sender: %s\n", SenderEmail)
	fmt.Println("Type 'exit' or 'quit' to stop.")
	fmt.Println("-------------------------------------------------")

	for {
		// 1. Prompt for input
		fmt.Print("\nPaste Target Email > ")
		scanner.Scan()
		recipient := strings.TrimSpace(scanner.Text())

		// 2. Check for exit
		if recipient == "exit" || recipient == "quit" || recipient == "" {
			fmt.Println("Exiting...")
			break
		}

		// 3. Construct the message
		subject := "Regarding your Junior Accountant role (A faster option?)"

		// We must manually build the MIME headers for HTML email
		msg := "From: " + SenderEmail + "\r\n" +
			"To: " + recipient + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/html; charset=\"UTF-8\"\r\n" +
			"\r\n" +
			htmlBody

		// 4. Send the email
		fmt.Printf("Sending to %s... ", recipient)
		err := smtp.SendMail(SMTPServer+":"+SMTPPort, auth, SenderEmail, []string{recipient}, []byte(msg))

		if err != nil {
			log.Printf("FAILED! ❌ Error: %v\n", err)
		} else {
			log.Println("SENT! ✅")
		}
	}
}
