package main

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(recipeCmd)
}

var recipeCmd = &cobra.Command{
	Use:   "recipe",
	Short: "Gives a good recipe for erwetensoep",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return errors.New("no arguments are expected")
		}
		fmt.Println(`
Ingredients:

500 gram of split peas
  2 decent sized boneless pork chops
200 gram of thick pork belly strips
  2 liter of water
  2 medium sized onions
  2 leeks
900 gram celeriac
180 gram carrots
7.5 gram of fresh thyme
  2 dryed bay leaves
  2 beef Kielbasa sausages (NL: Rookworst)

All of this can be obtained at the Albert Hein supermarket in the Netherlands
or at the Wholefoods in the US.

Step 1)
Bring the peas to a boil along with the pork shoulder chops, pork belly
strips, and water. Let it simmer on low heat for 30 minutes. Regularly
skim the foam from the soup and stir well. Frequently scrape the bottom of
the pan to prevent the soup from burning.

Step 2)
Meanwhile, finely chop the onion. Wash the leek and cut it into thin rings.
Peel and dice the celery root into 1 cm cubes. Peel the carrot, cut it
lengthwise into quarters, and then into 1 cm cubes. Strip the thyme leaves
from the stems.

Step 3)
Add the onion, leek, celeriac, carrot, bay leaf, and thyme to the soup, and
let it simmer gently for 30 minutes with the lid on the pan. Season with
pepper and, if desired, salt.

Step 4)
Remove the pork shoulder chops and pork belly strips from the pan and cut the
meat into small cubes. Slice the smoked sausage and stir it into the soup along
with the meat. Let the soup simmer gently for another 15 minutes. Divide the
soup into bowls and serve.

Recipe courtsey of https://www.ah.nl/allerhande/recept/R-R1193583/erwtensoep.
Translation by ChatGPT 4o.
`)

		return nil
	},
}
