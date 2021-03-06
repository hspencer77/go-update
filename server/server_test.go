package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"github.com/brave/go-update/controller"
	"github.com/brave/go-update/extension"
	"github.com/brave/go-update/extension/extensiontest"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var newExtension1 = extension.Extension{}
var newExtension2 = extension.Extension{}
var handler http.Handler

func init() {
	newExtensionID1 := "newext1eplbcioakkpcpgfkobkghlhen"
	newExtension1 = extension.Extension{
		ID:          newExtensionID1,
		Blacklisted: false,
		SHA256:      "4c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618",
		Title:       "test",
		Version:     "1.0.0",
	}
	newExtensionID2 := "newext2eplbcioakkpcpgfkobkghlhen"
	newExtension2 = extension.Extension{
		ID:          newExtensionID2,
		Blacklisted: false,
		SHA256:      "3c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618",
		Title:       "test",
		Version:     "1.0.0",
	}

	// Setup refreshing extensions with a new extension that we'll check for later
	// We maintain a count to make sure the refresh function is called more than just
	// the first time.
	count := 0
	controller.AllExtensionsMap = extension.LoadExtensionsIntoMap(&extension.OfferedExtensions)
	controller.ExtensionUpdaterTimeout = time.Millisecond * 1
	handler = chi.ServerBaseContext(setupRouter(setupLogger(context.Background())))
	controller.RefreshExtensionsTicker(func() {
		count++
		if count == 1 {
			controller.AllExtensionsMap[newExtensionID1] = newExtension1
		} else if count == 2 {
			controller.AllExtensionsMap[newExtensionID2] = newExtension2
		}
	})
}

func TestPing(t *testing.T) {
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := http.Get(server.URL)
	assert.Nil(t, err)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Received non-200 response: %d\n", resp.StatusCode)
	}
	expected := "."
	actual, err := ioutil.ReadAll(resp.Body)
	assert.Nil(t, err)
	if expected != string(actual) {
		t.Errorf("Expected the message '%s'\n", expected)
	}
}

func testCall(t *testing.T, server *httptest.Server, method string, query string,
	requestBody string, expectedResponseCode int, expectedResponse string, redirectLocation string) {
	extensionsURL := fmt.Sprintf("%s/extensions%s", server.URL, query)
	req, err := http.NewRequest(method, extensionsURL, bytes.NewBuffer([]byte(requestBody)))
	assert.Nil(t, err)
	req.Header.Add("Content-Type", "application/xml")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	assert.Nil(t, err)

	assert.Equal(t, expectedResponseCode, resp.StatusCode)

	// If this is a redirect, ensure the protocol is HTTPS.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		assert.Equal(t, redirectLocation, location)
	}

	actual, err := ioutil.ReadAll(resp.Body)
	assert.Nil(t, err)

	assert.Equal(t, expectedResponse, strings.TrimSpace(string(actual)))
}

func TestUpdateExtensions(t *testing.T) {
	server := httptest.NewServer(handler)
	defer server.Close()

	// No extensions
	requestBody := `
		<?xml version="1.0" encoding="UTF-8"?>
		<request protocol="3.0" version="chrome-53.0.2785.116" prodversion="53.0.2785.116" requestid="{b4f77b70-af29-462b-a637-8a3e4be5ecd9}" lang="" updaterchannel="stable" prodchannel="stable" os="mac" arch="x64" nacl_arch="x86-64">
		  <hw physmemory="16"/>
		  <os platform="Mac OS X" version="10.11.6" arch="x86_64"/>
		</request>`
	expectedResponse := "<response protocol=\"3.1\" server=\"prod\"></response>"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Unsupported protocol version
	requestBody =
		`<?xml version="1.0" encoding="UTF-8"?>
		<request protocol="2.0" version="chrome-53.0.2785.116" prodversion="53.0.2785.116" requestid="{b4f77b70-af29-462b-a637-8a3e4be5ecd9}" lang="" updaterchannel="stable" prodchannel="stable" os="mac" arch="x64" nacl_arch="x86-64">
			<app appid="aomjjhallfgjeglblehebfpbcfeobpgk">
				<updatecheck codebase="https://brave-core-ext.s3.brave.com/release/aomjjhallfgjeglblehebfpbcfeobpgk/extension_4_5_9_90.crx" version="4.5.9.90"/>
			</app>
		</request>`
	expectedResponse = "Error reading body request version: 2.0 not supported"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	// Not XML
	requestBody = "For the king!"
	expectedResponse = "Error reading body EOF"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	// Malformed XML
	requestBody = "<This way! No, that way!"
	expectedResponse = "Error reading body XML syntax error on line 1: attribute name without = in element"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	// Different XML schema
	requestBody = "<text>For the alliance!</text>"
	expectedResponse = "Error reading body expected element type <request> but have <text>"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	// Empty body request
	requestBody = ""
	expectedResponse = "Error reading body EOF"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	lightThemeExtension := extensiontest.ExtensionRequestFnFor("ldimlcelhnjgpjjemdjokpgeeikdinbm")

	// Single extension out of date
	requestBody = lightThemeExtension("0.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="ldimlcelhnjgpjjemdjokpgeeikdinbm">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/ldimlcelhnjgpjjemdjokpgeeikdinbm/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="1c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Single extension same version
	requestBody = lightThemeExtension("1.0.0")
	expectedResponse = "<response protocol=\"3.1\" server=\"prod\"></response>"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Single extension greater version
	requestBody = lightThemeExtension("2.0.0")
	expectedResponse = "<response protocol=\"3.1\" server=\"prod\"></response>"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	lightAndDarkThemeRequest := extensiontest.ExtensionRequestFnForTwo("ldimlcelhnjgpjjemdjokpgeeikdinbm", "bfdgpgibhagkpdlnjonhkabjoijopoge")

	// Multiple components with none out of date
	requestBody = lightAndDarkThemeRequest("70.0.0", "70.0.0")
	expectedResponse = "<response protocol=\"3.1\" server=\"prod\"></response>"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Only one components out of date
	requestBody = lightAndDarkThemeRequest("0.0.0", "70.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="ldimlcelhnjgpjjemdjokpgeeikdinbm">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/ldimlcelhnjgpjjemdjokpgeeikdinbm/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="1c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Other component of 2 out of date
	requestBody = lightAndDarkThemeRequest("70.0.0", "0.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="bfdgpgibhagkpdlnjonhkabjoijopoge">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/bfdgpgibhagkpdlnjonhkabjoijopoge/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="ae517d6273a4fc126961cb026e02946db4f9dbb58e3d9bc29f5e1270e3ce9834" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Both components need updates
	requestBody = lightAndDarkThemeRequest("0.0.0", "0.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="ldimlcelhnjgpjjemdjokpgeeikdinbm">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/ldimlcelhnjgpjjemdjokpgeeikdinbm/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="1c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
    <app appid="bfdgpgibhagkpdlnjonhkabjoijopoge">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/bfdgpgibhagkpdlnjonhkabjoijopoge/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="ae517d6273a4fc126961cb026e02946db4f9dbb58e3d9bc29f5e1270e3ce9834" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Unkonwn extension ID goes to Google server
	requestBody = extensiontest.ExtensionRequestFnFor("aaaaaaaaaaaaaaaaaaaa")("0.0.0")
	expectedResponse = ""
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusTemporaryRedirect, expectedResponse, "https://update.googleapis.com/service/update2?braveRedirect=true")

	// Unkonwn extension ID goes to Google server and preserves queyr params
	requestBody = extensiontest.ExtensionRequestFnFor("aaaaaaaaaaaaaaaaaaaa")("0.0.0")
	expectedResponse = ""
	testCall(t, server, http.MethodPost, "?test=hi", requestBody, http.StatusTemporaryRedirect, expectedResponse, "https://update.googleapis.com/service/update2?test=hi&braveRedirect=true")

	// Make sure a huge request body does not crash the server
	data := make([]byte, 1024*1024*11) // 11 MiB
	_, err := rand.Read(data)
	assert.Nil(t, err)
	requestBody = string(data)
	expectedResponse = "Request too large"
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusBadRequest, expectedResponse, "")

	// Single new extension out of date that was added in by the refresh timer
	requestBody = extensiontest.ExtensionRequestFnFor("newext1eplbcioakkpcpgfkobkghlhen")("0.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="newext1eplbcioakkpcpgfkobkghlhen">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/newext1eplbcioakkpcpgfkobkghlhen/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="4c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")

	// Single second new extension out of date that was added in by the refresh timer
	requestBody = extensiontest.ExtensionRequestFnFor("newext2eplbcioakkpcpgfkobkghlhen")("0.0.0")
	expectedResponse = `<response protocol="3.1" server="prod">
    <app appid="newext2eplbcioakkpcpgfkobkghlhen">
        <updatecheck status="ok">
            <urls>
                <url codebase="https://brave-core-ext.s3.brave.com/release/newext2eplbcioakkpcpgfkobkghlhen/extension_1_0_0.crx"></url>
            </urls>
            <manifest version="1.0.0">
                <packages>
                    <package name="extension_1_0_0.crx" hash_sha256="3c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618" required="true"></package>
                </packages>
            </manifest>
        </updatecheck>
    </app>
</response>`
	testCall(t, server, http.MethodPost, "", requestBody, http.StatusOK, expectedResponse, "")
}

func getQueryParams(extension *extension.Extension) string {
	return `x=id%3D` + extension.ID + `%26v%3D` + extension.Version
}

func TestWebStoreUpdateExtension(t *testing.T) {
	server := httptest.NewServer(handler)
	defer server.Close()

	allExtensionsMap := extension.LoadExtensionsIntoMap(&extension.OfferedExtensions)

	// Empty query param request, no extensions.
	requestBody := ""
	query := ""
	expectedResponse := `<gupdate protocol="3.1" server="prod"></gupdate>`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusOK, expectedResponse, "")

	// Extension that we handle which is outdated should produce a response
	outdatedLightThemeExtension, ok := allExtensionsMap["ldimlcelhnjgpjjemdjokpgeeikdinbm"]
	outdatedLightThemeExtension.Version = "0.0.0"
	assert.True(t, ok)
	query = "?" + getQueryParams(&outdatedLightThemeExtension)
	expectedResponse = `<gupdate protocol="3.1" server="prod">
    <app appid="ldimlcelhnjgpjjemdjokpgeeikdinbm" status="ok">
        <updatecheck status="ok" codebase="https://brave-core-ext.s3.brave.com/release/ldimlcelhnjgpjjemdjokpgeeikdinbm/extension_1_0_0.crx" version="1.0.0" hash_sha256="1c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618"></updatecheck>
    </app>
</gupdate>`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusOK, expectedResponse, "")

	// Multiple extensions that we handle which are outdated should produce a response
	outdatedDarkThemeExtension, ok := allExtensionsMap["bfdgpgibhagkpdlnjonhkabjoijopoge"]
	assert.True(t, ok)
	outdatedDarkThemeExtension.Version = "0.0.0"
	query = "?" + getQueryParams(&outdatedLightThemeExtension) + "&" + getQueryParams(&outdatedDarkThemeExtension)
	expectedResponse = `<gupdate protocol="3.1" server="prod">
    <app appid="ldimlcelhnjgpjjemdjokpgeeikdinbm" status="ok">
        <updatecheck status="ok" codebase="https://brave-core-ext.s3.brave.com/release/ldimlcelhnjgpjjemdjokpgeeikdinbm/extension_1_0_0.crx" version="1.0.0" hash_sha256="1c714fadd4208c63f74b707e4c12b81b3ad0153c37de1348fa810dd47cfc5618"></updatecheck>
    </app>
    <app appid="bfdgpgibhagkpdlnjonhkabjoijopoge" status="ok">
        <updatecheck status="ok" codebase="https://brave-core-ext.s3.brave.com/release/bfdgpgibhagkpdlnjonhkabjoijopoge/extension_1_0_0.crx" version="1.0.0" hash_sha256="ae517d6273a4fc126961cb026e02946db4f9dbb58e3d9bc29f5e1270e3ce9834"></updatecheck>
    </app>
</gupdate>`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusOK, expectedResponse, "")

	// Extension that we handle which is up to date should NOT produce an update but still be successful
	lightThemeExtension, ok := allExtensionsMap["ldimlcelhnjgpjjemdjokpgeeikdinbm"]
	assert.True(t, ok)
	query = "?" + getQueryParams(&lightThemeExtension)
	expectedResponse = `<gupdate protocol="3.1" server="prod"></gupdate>`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusOK, expectedResponse, "")

	// Unkonwn extension ID goes to Google server
	unknownExtension := extension.Extension{
		ID:      "aaaaaaaaaaaaaaaaaaaa",
		Version: "0.0.0",
	}
	query = "?" + getQueryParams(&unknownExtension)
	expectedResponse = `<a href="https://clients2.google.com/service/update2/crx?x=id%3Daaaaaaaaaaaaaaaaaaaa%26v%3D0.0.0&amp;braveRedirect=true">Temporary Redirect</a>.`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusTemporaryRedirect, expectedResponse, "https://clients2.google.com/service/update2/crx?x=id%3Daaaaaaaaaaaaaaaaaaaa%26v%3D0.0.0&braveRedirect=true")

	// Unkonwn extension ID with multiple extensions, we try to handle ourselves.
	unknownExtension = extension.Extension{
		ID:      "aaaaaaaaaaaaaaaaaaaa",
		Version: "0.0.0",
	}
	unknownExtension2 := extension.Extension{
		ID:      "bbaaaaaaaaaaaaaaaaaa",
		Version: "0.0.0",
	}
	query = "?" + getQueryParams(&unknownExtension) + "&" + getQueryParams(&unknownExtension2)
	expectedResponse = `<gupdate protocol="3.1" server="prod"></gupdate>`
	testCall(t, server, http.MethodGet, query, requestBody, http.StatusOK, expectedResponse, "")
}

func TestPrintExtensions(t *testing.T) {
	server := httptest.NewServer(handler)
	defer server.Close()

	testURL := fmt.Sprintf("%s/extensions/test", server.URL)
	req, err := http.NewRequest(http.MethodGet, testURL, bytes.NewBuffer([]byte("")))
	assert.Nil(t, err)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	actual, err := ioutil.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.True(t, strings.Contains(string(actual), "ldimlcelhnjgpjjemdjokpgeeikdinbm"))

	// Clear out the extensions map.
	controller.AllExtensionsMap = map[string]extension.Extension{}
	resp, err = client.Do(req)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	actual, err = ioutil.ReadAll(resp.Body)
	assert.Nil(t, err)
	assert.Equal(t, string(actual), "No extensions found, do you have the AWS config correct for DynamoDB?")
}
