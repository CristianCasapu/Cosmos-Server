package utils



var Template = `From: %s
To: %s
Subject: %s
MIME-Version: 1.0
Content-Type: text/html; charset=UTF-8

<!DOCTYPE html>
<html>
<head>
<style>
		body {
				font-family: Arial, sans-serif;
				line-height: 1.6;
		}
		.container {
				max-width: 500px;
				margin: auto;
				margin-top: 30px;
				padding: 20px;
		}
		.header {
				text-align: center;
				padding-bottom: 10px;
		}
		.logo {
				width: 130px;
		}
		.content {
				background-color: #f4f4f4;
				padding: 20px;
				border-radius: 5px;
				border-top: 2px solid rgb(171, 71, 188);
		}
		.footer {
			padding-top: 20px;
			color: #999;
		}
		h1 {
			color: rgb(171, 71, 188);
		}
		.button {
			background: rgb(171, 71, 188);
			color: white !important;
			padding: 10px 20px;
			border-radius: 5px;
			display: inline-block;
			margin-top: 15px;
			text-decoration: none;
		}
		.button:hover {
			background: rgb(141, 41, 168);
		}
</style>
</head>
<body>
<div class="container">
		<div class="header">
				<img src="%s" alt="Logo" class="logo">
		</div>
		<div class="content">
				%s
		</div>
		<div class="footer">
			Sent from: %s
		</div>
</div>
</body>
</html>`

func IsEmailEnabled() bool {
	config := GetMainConfig()
	return config.EmailConfig.Enabled
}

func IsNotifyLoginEmailEnabled() bool {
	config := GetMainConfig()
	return config.EmailConfig.NotifyLogin
}

func SendEmail(recipients []string, subject string, body string) error {
	config := GetMainConfig()

	msg := BuildEmailMessage(config.EmailConfig.From, recipients, subject, body)

	TriggerEvent(
		"cosmos.email.send",
		"Email sent",
		"success",
		"",
		map[string]interface{}{
			"recipients": recipients,
			"subject":    subject,
		})

	// Delivery lives in emails_deliver.go (timeouts, implicit TLS on 465).
	_, err := DeliverEmail(config.EmailConfig, config.EmailConfig.From, recipients, msg)
	return err
}
