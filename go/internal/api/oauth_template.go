package api

import (
	"html/template"
	"net/http"
)

const oauthTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Connection Status</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background-color: #0d0d0d;
            color: #ffffff;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            text-align: center;
            overflow: hidden;
        }
        .container {
            background: rgba(255, 255, 255, 0.05);
            border: 1px solid rgba(255, 255, 255, 0.1);
            padding: 40px;
            border-radius: 24px;
            box-shadow: 0 10px 30px rgba(0, 0, 0, 0.5);
            backdrop-filter: blur(10px);
        }
        .icon {
            width: 80px;
            height: 80px;
            /* Update background/box-shadow color dynamically based on state */
            {{ if eq .State "SUCCESS" }}
            background: rgba(57, 255, 20, 0.1); 
            box-shadow: 0 0 30px rgba(57, 255, 20, 0.2);
            {{ else if eq .State "ERROR" }}
            background: rgba(255, 57, 20, 0.1); 
            box-shadow: 0 0 30px rgba(255, 57, 20, 0.2);
            {{ else }}
            background: rgba(255, 200, 20, 0.1); 
            box-shadow: 0 0 30px rgba(255, 200, 20, 0.2);
            {{ end }}
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 0 auto 24px;
        }
        .icon svg {
            width: 48px;
            height: 48px;
            /* Update SVG color dynamically based on state */
            {{ if eq .State "SUCCESS" }}
            color: #39FF14; 
            {{ else if eq .State "ERROR" }}
            color: #FF3914; 
            {{ else }}
            color: #FFC814; 
            {{ end }}
        }
        h1 {
            font-size: 24px;
            font-weight: 900;
            margin: 0 0 12px;
            letter-spacing: 0.5px;
        }
        p {
            font-size: 14px;
            color: rgba(255, 255, 255, 0.6);
            margin: 0;
        }
    </style>
</head>
<body>
    <div class="container">
        <!-- Render Icon dynamically (Checkmark for success, X for error) -->
        <div class="icon">
            {{ if eq .State "SUCCESS" }}
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"></path>
                <polyline points="22 4 12 14.01 9 11.01"></polyline>
            </svg>
            {{ else if eq .State "ERROR" }}
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="10"></circle>
                <line x1="15" y1="9" x2="9" y2="15"></line>
                <line x1="9" y1="9" x2="15" y2="15"></line>
            </svg>
            {{ else }}
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                <line x1="12" y1="9" x2="12" y2="13"></line>
                <line x1="12" y1="17" x2="12.01" y2="17"></line>
            </svg>
            {{ end }}
        </div>
        
        <!-- Render Headline and Subtext dynamically -->
        {{ if eq .State "SUCCESS" }}
        <h1>Connection Successful</h1>
        <p>You may close this tab and return to the application.</p>
        {{ else if eq .State "ERROR" }}
        <h1>Connection Failed</h1>
        <p>{{ .ErrorMessage }}</p>
        {{ else }}
        <h1>Invalid Request</h1>
        <p>Please start the connection process from the desktop app.</p>
        {{ end }}
    </div>

    <!-- ONLY RENDER THIS SCRIPT BLOCK IF STATE == SUCCESS -->
    {{ if eq .State "SUCCESS" }}
    <script>
        // Attempt to redirect to the desktop app using custom protocol schema
        try {
            window.location.href = "toro://auth-success";
        } catch (e) {
            console.error("Failed to redirect to deeply-linked app:", e);
        }
        
        // Attempt self-destruct (auto-close tab) after 500ms
        setTimeout(() => {
            try {
                window.close();
            } catch (e) {
                console.error("Tab auto-close blocked by browser. User must close manually.", e);
            }
        }, 500);
    </script>
    {{ end }}
</body>
</html>`

var oauthTemplate = template.Must(template.New("oauth").Parse(oauthTemplateStr))

type OAuthTemplateData struct {
	State        string // "SUCCESS", "ERROR", "INVALID_STATE"
	ErrorMessage string // Only used when State is "ERROR"
}

func renderOAuthCallbackPage(w http.ResponseWriter, data OAuthTemplateData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = oauthTemplate.Execute(w, data)
}
