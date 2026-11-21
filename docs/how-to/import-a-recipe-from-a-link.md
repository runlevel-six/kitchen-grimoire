# How to import a recipe from a link

Most recipe sites publish their recipes as structured data (schema.org
`Recipe`). Kitchen Grimoire reads that data, so you can paste a link and
get the title, ingredients, steps, times, and photo without retyping.

## Import a page

1. Tap **Import** at the top of any page.
2. Paste the link into **Recipe link** and tap **Get recipe**. You can
   leave off `https://`.
3. The recipe opens in the editor, titled **Check and save**, with a note
   saying which site it came from. **Nothing is saved yet.**
4. Check it over. Fix anything that came across oddly, and fill in what the
   site couldn't know: **From (who gave it to us)**, **Collection**,
   **Categories**, **Tags**. See [Add and edit recipes](add-and-edit-recipes.md)
   for the editor's conventions.
5. Tap **Save**. The page's main photo is fetched and saved with the
   recipe.

If the photo can't be fetched or is in a format the app can't read (such
as AVIF), the recipe is still saved, without it. You can add one later
from **Edit**.

## Already imported?

If a recipe from the same page (compared by the page's canonical address)
is already in the library, the editor warns "This recipe is already in
your library", with a link to it. Saving anyway makes a second copy.

## What gets kept

Besides the fields you see, an imported recipe keeps:

- the source link and the site's name;
- the structured data exactly as the page published it (`raw_source`),
  so a later version of the app can re-read it;
- review flags, if the page was missing something (below).

## When it doesn't work

| Message | What happened | What to do |
|---|---|---|
| That site refused our request (it blocks servers). | The site answered 401, 403, 429, or 503. Some sites block server requests, sometimes only after a few. | Type the recipe in. Don't retry repeatedly; that's what gets servers blocked. |
| No recipe found on that page. | The page has no recipe data and not even a title. | Type it in. |
| That address isn't allowed. Only public http(s) sites can be imported. | The link points at a private, local, or otherwise blocked address, or uses a port other than 80 or 443. | This is deliberate: see [Security model](../explanation/security-model.md#ssrf-guard). |
| That address isn't a web page. | The link is to an image, PDF, or other file. | Type it in. |
| That site took too long to answer. | Over 15 seconds (`GRIMOIRE_FETCH_TIMEOUT`). | Try again in a moment. |

The "blocked" message suggests sharing the recipe from your phone to
Kitchen Grimoire. **That is not yet available:** browser capture (a
bookmarklet, and an iOS Shortcut for the share sheet) is planned for
Phase 5.

## Pages without full recipe data

The app tries, in order: JSON-LD, then Microdata, then the page's title,
description, and image (OpenGraph). In the last case you get a **stub**:
the title and description filled in, the rest empty, and the recipe
flagged for review with the reason `no_structured_data`. Fill in the
ingredients and steps by hand.

Other review reasons from a URL import are `no_ingredients`, `no_steps`,
`no_title`, and `truncated`. Flagged recipes appear in the
[review queue](review-imported-recipes.md).

## For operators

Each attempt is counted in `grimoire_scrape_total{result=...}` and logged
with the site's domain (never in metrics). See [Metrics](../reference/metrics.md).
The server identifies itself honestly as `KitchenGrimoire/<version>`; it
never pretends to be a browser and never retries a refusal.
