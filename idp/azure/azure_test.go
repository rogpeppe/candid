// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package azure_test

import (
	gc "gopkg.in/check.v1"
	"gopkg.in/yaml.v2"

	"github.com/CanonicalLtd/blues-identity/config"
)

type azureSuite struct{}

var _ = gc.Suite(&azureSuite{})

var configTests = []struct {
	about       string
	yaml        string
	expectError string
}{{
	about: "good config",
	yaml: `
identity-providers:
 - type: azure
   client-id: client-001
   client-secret: secret-001
`,
}, {
	about: "no client-id",
	yaml: `
identity-providers:
 - type: azure
   client-secret: secret-001
`,
	expectError: `cannot unmarshal azure configuration: client-id not specified`,
}, {
	about: "no client-secret",
	yaml: `
identity-providers:
 - type: azure
   client-id: client-001
`,
	expectError: `cannot unmarshal azure configuration: client-secret not specified`,
}}

func (s *azureSuite) TestConfig(c *gc.C) {
	for i, test := range configTests {
		c.Logf("test %d. %s", i, test.about)
		var conf config.Config
		err := yaml.Unmarshal([]byte(test.yaml), &conf)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.Equals, nil)
		c.Assert(conf.IdentityProviders, gc.HasLen, 1)
		c.Assert(conf.IdentityProviders[0].Name(), gc.Equals, "azure")
	}
}
