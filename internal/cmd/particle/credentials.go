package particle

import (
	"fmt"
	"strings"

	"github.com/partite-ai/partite-cli/internal/cmdutil"
	gql "github.com/partite-ai/partite-cli/internal/graphql"
	"github.com/spf13/cobra"
)

// Credential is one credential configured on an installed particle. At most
// one of the *_wo fields, or oauth2_exchange_id, carries the secret; a
// credential with none of them names what is stored and changes nothing.
type Credential struct {
	Name                    string                   `json:"name"`
	Method                  string                   `json:"method"`
	APIKey                  *APIKeyCredential        `json:"apikey_wo,omitempty"`
	Basic                   *BasicCredential         `json:"basic_wo,omitempty"`
	OAuth2ClientCredentials *OAuth2ClientCredentials `json:"oauth2_client_credentials_wo,omitempty"`
	Raw                     *RawCredential           `json:"raw_wo,omitempty"`
	SigningKey              *SigningKeyCredential    `json:"signing_key_wo,omitempty"`
	// OAuth2ExchangeID names a finished partite particle authorize exchange.
	OAuth2ExchangeID string `json:"oauth2_exchange_id,omitempty"`
}

type APIKeyCredential struct {
	Key string `json:"key"`
}

type BasicCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type OAuth2ClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	TokenURL     string `json:"token_url,omitempty"`
}

type RawCredential struct {
	Value string `json:"value"`
}

type SigningKeyCredential struct {
	Algorithm string `json:"algorithm"`
	Key       string `json:"key"`
}

// input returns the API input for c, or false when c has no secret.
func (c Credential) input() (credentialInput, bool) {
	in := credentialInput{ParticleCredentialInput: gql.ParticleCredentialInput{CredentialName: c.Name, Method: c.Method}}
	switch {
	case c.APIKey != nil:
		in.Apikey = &gql.ApiKeyParticleCredentialInput{Key: c.APIKey.Key}
	case c.Basic != nil:
		in.Basic = &gql.BasicParticleCredentialInput{Username: c.Basic.Username, Password: c.Basic.Password}
	case c.OAuth2ClientCredentials != nil:
		o := c.OAuth2ClientCredentials
		in.OAuth2ClientCredentials = &gql.OAuth2ClientCredentialsParticleCredentialInput{ClientID: o.ClientID, ClientSecret: o.ClientSecret, TokenUrl: cmdutil.OptString(o.TokenURL)}
	case c.Raw != nil:
		in.Raw = &gql.RawParticleCredentialInput{Value: c.Raw.Value}
	case c.SigningKey != nil:
		in.SigningKey = &gql.SigningKeyParticleCredentialInput{Algorithm: c.SigningKey.Algorithm, Key: c.SigningKey.Key}
	case c.OAuth2ExchangeID != "":
		in.OAuth2ExchangeID = c.OAuth2ExchangeID
	default:
		return in, false
	}
	return in, true
}

// credentialFlags set one credential from flags.
type credentialFlags struct {
	credentials   []string
	apiKey        cmdutil.SecretFlag
	username      string
	password      cmdutil.SecretFlag
	clientID      string
	clientSecret  cmdutil.SecretFlag
	tokenURL      string
	rawValue      cmdutil.SecretFlag
	signingKey    cmdutil.SecretFlag
	algorithm     string
	oauthExchange string
}

func (f *credentialFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&f.credentials, "credential", nil, "credential to set, as name=method, or name when it has one method; repeat for more")
	cmdutil.AddSecretFlags(cmd, &f.apiKey, "api-key", "key, for an APIKEY method")
	cmd.Flags().StringVar(&f.username, "username", "", "user name, for a BASIC method")
	cmdutil.AddSecretFlags(cmd, &f.password, "password", "password, for a BASIC method")
	cmd.Flags().StringVar(&f.clientID, "client-id", "", "OAuth client id, for an OAUTH2 method with the client-credentials grant")
	cmdutil.AddSecretFlags(cmd, &f.clientSecret, "client-secret", "OAuth client secret, for an OAUTH2 method")
	cmd.Flags().StringVar(&f.tokenURL, "token-url", "", "OAuth token URL, when the particle declares none")
	cmdutil.AddSecretFlags(cmd, &f.rawValue, "raw-value", "value, for a RAW method")
	cmdutil.AddSecretFlags(cmd, &f.signingKey, "signing-key", "private key, for a SIGNING_KEY method")
	cmd.Flags().StringVar(&f.algorithm, "algorithm", "", "signing algorithm, for a SIGNING_KEY method (default: the particle's)")
	cmd.Flags().StringVar(&f.oauthExchange, "oauth-exchange", "", "id of a finished partite particle authorize exchange, for an OAUTH2 method")
}

func (f *credentialFlags) secretGiven() bool {
	return f.apiKey.Given() || f.username != "" || f.password.Given() || f.clientID != "" || f.clientSecret.Given() ||
		f.tokenURL != "" || f.rawValue.Given() || f.signingKey.Given() || f.algorithm != "" || f.oauthExchange != ""
}

func (f *credentialFlags) given() bool { return len(f.credentials) > 0 || f.secretGiven() }

// merge adds the credentials named by the flags to list, replacing any of the
// same name. With prompt, it also asks on a terminal for required credentials
// that are still missing.
func (f *credentialFlags) merge(app *cmdutil.App, art Artifact, list []Credential, prompt bool) ([]Credential, error) {
	refs := f.credentials
	if len(refs) == 0 && f.secretGiven() {
		if len(art.Credentials) != 1 {
			return nil, cmdutil.UsageError("pass --credential to say which credential the secret flags are for")
		}
		refs = []string{art.Credentials[0].Name}
	}
	if len(refs) > 1 && f.secretGiven() {
		return nil, cmdutil.UsageError("the secret flags work with one --credential; use -f for several, or leave them out to be asked")
	}
	set := func(c Credential) {
		for i := range list {
			if list[i].Name == c.Name {
				list[i] = c
				return
			}
		}
		list = append(list, c)
	}
	for _, ref := range refs {
		name, method, _ := strings.Cut(ref, "=")
		c, err := f.build(app, art, name, method)
		if err != nil {
			return nil, err
		}
		set(c)
	}
	if !prompt || !app.Interactive() {
		return list, nil
	}
	for _, decl := range art.Credentials {
		if !decl.Required || hasCredential(list, decl.Name) {
			continue
		}
		fmt.Fprintf(app.Err, "The particle needs credential %s.\n", decl.Name)
		empty := &credentialFlags{}
		c, err := empty.build(app, art, decl.Name, "")
		if err != nil {
			return nil, err
		}
		set(c)
	}
	return list, nil
}

func hasCredential(list []Credential, name string) bool {
	for _, c := range list {
		if c.Name == name {
			return true
		}
	}
	return false
}

// build makes the credential name with the given method, or the credential's
// only method when method is "". Secrets missing from the flags are asked for
// on a terminal.
func (f *credentialFlags) build(app *cmdutil.App, art Artifact, name, method string) (Credential, error) {
	decl, m, err := findMethod(art, name, method)
	if err != nil {
		return Credential{}, err
	}
	c := Credential{Name: decl.Name, Method: m.Name}
	forName := " for " + decl.Name
	switch m.Type {
	case string(gql.ParticleCredentialMethodTypeApikey):
		key, err := f.apiKey.Read(app, "api-key", "API key"+forName)
		if err != nil {
			return Credential{}, err
		}
		c.APIKey = &APIKeyCredential{Key: key}
	case string(gql.ParticleCredentialMethodTypeBasic):
		if f.username == "" {
			return Credential{}, cmdutil.UsageError("--username is required for credential %s", decl.Name)
		}
		pw, err := f.password.Read(app, "password", "Password"+forName)
		if err != nil {
			return Credential{}, err
		}
		c.Basic = &BasicCredential{Username: f.username, Password: pw}
	case string(gql.ParticleCredentialMethodTypeRaw):
		v, err := f.rawValue.Read(app, "raw-value", "Value"+forName)
		if err != nil {
			return Credential{}, err
		}
		c.Raw = &RawCredential{Value: v}
	case string(gql.ParticleCredentialMethodTypeSigningKey):
		alg := f.algorithm
		if alg == "" && m.SigningKey != nil {
			alg = m.SigningKey.Algorithm
		}
		key, err := f.signingKey.Read(app, "signing-key", "Private key"+forName)
		if err != nil {
			return Credential{}, err
		}
		c.SigningKey = &SigningKeyCredential{Algorithm: alg, Key: key}
	case string(gql.ParticleCredentialMethodTypeOauth2):
		switch {
		case f.oauthExchange != "":
			c.OAuth2ExchangeID = f.oauthExchange
		case f.clientID != "":
			secret, err := f.clientSecret.Read(app, "client-secret", "Client secret"+forName)
			if err != nil {
				return Credential{}, err
			}
			c.OAuth2ClientCredentials = &OAuth2ClientCredentials{ClientID: f.clientID, ClientSecret: secret, TokenURL: f.tokenURL}
		default:
			return Credential{}, &cmdutil.Error{
				Code: cmdutil.ExitUsage,
				Msg:  fmt.Sprintf("credential %s uses OAuth2 method %s; pass --client-id and --client-secret, or --oauth-exchange", decl.Name, m.Name),
				Hint: fmt.Sprintf("To log in through the browser, run: partite particle authorize --artifact %s --credential %s=%s --client-id <id>", art.ID, decl.Name, m.Name),
			}
		}
	default:
		return Credential{}, cmdutil.UsageError("credential %s method %s has type %s, which this CLI cannot set; use -f", decl.Name, m.Name, m.Type)
	}
	return c, nil
}

func findMethod(art Artifact, name, method string) (ArtifactCredential, CredentialMethod, error) {
	var names []string
	for _, decl := range art.Credentials {
		names = append(names, decl.Name)
		if decl.Name != name {
			continue
		}
		var methods []string
		for _, m := range decl.Methods {
			methods = append(methods, m.Name)
			if m.Name == method || (method == "" && len(decl.Methods) == 1) {
				return decl, m, nil
			}
		}
		if method == "" {
			return decl, CredentialMethod{}, cmdutil.UsageError("credential %s has several methods; pass --credential %s=<method> with one of %s", name, name, strings.Join(methods, ", "))
		}
		return decl, CredentialMethod{}, cmdutil.UsageError("credential %s has no method %s; use one of %s", name, method, strings.Join(methods, ", "))
	}
	if len(names) == 0 {
		return ArtifactCredential{}, CredentialMethod{}, cmdutil.UsageError("%s %s declares no credentials", art.Name, art.Version)
	}
	return ArtifactCredential{}, CredentialMethod{}, cmdutil.UsageError("%s %s has no credential %s; it has %s", art.Name, art.Version, name, strings.Join(names, ", "))
}
