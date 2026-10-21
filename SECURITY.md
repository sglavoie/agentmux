# Security

Agent Mux runs local agent commands and reads local session data. Reports about
unintended command execution, access to another session, credential disclosure,
or unsafe handling of untrusted session content are especially useful.

## Report a vulnerability

Use GitHub's **Security → Report a vulnerability** option when available in
this repository. If it is unavailable, open an issue asking the maintainer for
a private reporting channel, without including vulnerability details or
sensitive data. Do not attach exploit details or credentials to a public issue.

Include the affected revision (`amx --version`), operating system, reproduction
steps using synthetic data, expected behavior, and potential impact. Redact
credentials, private conversations, project paths, and account details from
logs and screenshots.

Security fixes currently target the latest code on `main`; there is no
separate long-term support branch or guaranteed response time.
