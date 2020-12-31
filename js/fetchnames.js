const cardNameUrl = "https://api.scryfall.com/catalog/card-names";
const cardQueryUrl = "https://api.scryfall.com/cards/named?fuzzy="


/*
 * Query Scryfall to retrieve the list of card names.
 */
async function fetchNames() {
    let cardNames = await fetch(cardNameUrl)
        // Transform the data into json
        .then(response => response.json())
        // Return the array present in .data
        .then(scryfallOutput => scryfallOutput.data);
    return cardNames;
}

async function fetchCard(query) {
	let cardEntry = await fetch(cardQueryUrl)
		// Transform the data into json
	    .then(response => response.json())
	    // Return the array present in .data
	    .then(scryfallOutput => scryfallOutput.data);
    return cardEntry;
}
