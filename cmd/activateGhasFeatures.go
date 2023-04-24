/*
Package cmd provides a command-line interface for changing GHAS settings for a given organization.
*/
package cmd

import (
	"log"
	"os"

	"github.com/gateixeira/gei-migration-helper/cmd/github"
	"github.com/spf13/cobra"
)

// migrateRepoCmd represents the migrateRepo command
var activateGhasFeaturesCmd = &cobra.Command{
	Use:   "activate-ghas-features",
	Short: "Activate GHAS features for all orgs in an enterprise",
	Run: func(cmd *cobra.Command, args []string) {
		enterprise, _ := cmd.Flags().GetString("enterprise")
		token, _ := cmd.Flags().GetString("token")
		organization, _ := cmd.Flags().GetString("organization")

		if (organization != "") {
			log.Println("[🔄] Activating GHAS settings for organization: " + organization)
			github.ChangeGHASOrgSettings(organization, true, token)
			log.Println("[✅] Done")
			os.Exit(0)
		}
		
		log.Println("[🔄] Fetching organizations from enterprise...")
		organizations, err := github.GetOrganizationsInEnterprise(enterprise, token)
		log.Println("[✅] Done")

		if err != nil {
			log.Println("[❌] Error fetching organizations from enterprise")
			os.Exit(1)
		}

		for _, organization := range organizations {
			log.Println("[🔄] Activating GHAS settings for organization: " + organization)
			github.ChangeGHASOrgSettings(organization, true, token)
			log.Println("[✅] Done")
		}
	},
}

func init() {
	rootCmd.AddCommand(activateGhasFeaturesCmd)

	activateGhasFeaturesCmd.Flags().String("enterprise", "", "The slug of the enterprise")
	activateGhasFeaturesCmd.Flags().String("token", "", "The access token")
	activateGhasFeaturesCmd.Flags().String("organization", "", "To filter for a single organization")
}