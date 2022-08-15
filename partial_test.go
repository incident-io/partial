package partial_test

import (
	"time"

	"github.com/incident-io/partial"
	"github.com/incident-io/partial/test"
	"gopkg.in/guregu/null.v3"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var _ = Describe("Partial", func() {
	Describe("NewPartial", func() {
		var (
			model partial.Partial[test.Incident]
			now   = time.Now()
		)

		JustBeforeEach(func() {
			var err error
			model, err = partial.New(&test.Incident{
				ID:             "id",
				OrganisationID: "org-id",
				Organisation: &test.Organisation{
					ID:   "id",
					Name: "Peanuts",
				},
				CreatedAt: now,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("only creates fields that are valid database columns", func() {
			Expect(model.FieldNames).To(ConsistOf(
				"ID",
				"OrganisationID",
				"CreatedAt",
			))
		})

		It("applies values from the original model", func() {
			var inc test.Incident
			Expect(*model.Apply(inc)).To(MatchFields(IgnoreExtras, Fields{
				"ID":             Equal("id"),
				"OrganisationID": Equal("org-id"),
				"Organisation":   BeNil(),
				"CreatedAt":      Equal(now),
			}))
		})
	})

	Describe("methods", func() {
		var (
			model partial.Partial[test.Organisation]
		)

		BeforeEach(func() {
			model = test.OrganisationBuilder(
				test.OrganisationBuilder.ID("id"),
				test.OrganisationBuilder.Name("name"),
				test.OrganisationBuilder.OptionalString(null.StringFrom("something-here")),
			)
		})

		Describe("Match", func() {
			var (
				other test.Organisation
				match bool
			)

			BeforeEach(func() {
				other = test.Organisation{
					ID:             "id",
					Name:           "name",
					OptionalString: null.StringFrom("something-here"),
					BoolFlag:       true,
				}
			})

			JustBeforeEach(func() {
				match = model.Match(&other)
			})

			Context("when all specified fields match", func() {
				It("returns true", func() {
					Expect(match).To(BeTrue())
				})
			})

			Context("when one of the fields does not match", func() {
				BeforeEach(func() {
					other.OptionalString = null.StringFrom("something-else-here")
				})

				It("returns false", func() {
					Expect(match).To(BeFalse())
				})
			})
		})

		Describe("Apply", func() {
			var (
				base    test.Organisation
				patched *test.Organisation
			)

			BeforeEach(func() {
				base = test.Organisation{
					ID:             "base-id",
					Name:           "base-name",
					OptionalString: null.StringFrom("something-here"),
					BoolFlag:       true,
				}
			})

			JustBeforeEach(func() {
				patched = model.Apply(base)
			})

			It("sets all fields from the tracked model in the result", func() {
				Expect(patched).To(test.OrganisationMatcher(
					test.OrganisationMatcher.ID("id"),
					test.OrganisationMatcher.Name("name"),
					test.OrganisationMatcher.OptionalString(null.StringFrom("something-here")),
				))
			})

			It("preserves fields from the base that are not in the tracked model untouched", func() {
				Expect(patched).To(test.OrganisationMatcher(
					test.OrganisationMatcher.BoolFlag(true),
				))
			})
		})
	})
})
