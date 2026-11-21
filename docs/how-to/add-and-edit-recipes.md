# How to add and edit recipes

This guide covers typing a recipe in, the editor's conventions (groups,
steps, categories, tags), photos, and undoing mistakes. To start from a web
page instead, see [Import a recipe from a link](import-a-recipe-from-a-link.md).

## Add a recipe

1. Tap **+ New** at the top of any page.
2. Fill in at least the **Title**. Everything else is optional.
3. Tap **Save**. The recipe opens, with "Recipe added."

The editor works without JavaScript, so it behaves the same on any phone
or browser.

## Fields

| Field | What to put there |
|---|---|
| Title | The recipe's name. Required, up to 200 characters. |
| Description | A sentence or two. |
| Makes | The yield as you'd say it: `24 bars`, `2 loaves`, `serves 6`. |
| Servings | A number for the scaler, e.g. `24`. When set, scaling shows "Makes 48 (2× 24 bars)". |
| Prep, Cook, Total (min) | Whole minutes. Shown as "1 hr 15 min". |
| Ingredients | One per line, in groups (below). |
| Steps | Blank line between steps, in groups (below). |
| Notes | Anything else: substitutions, who loves it, what went wrong last time. Plain text; line breaks are kept. |
| From (who gave it to us) | The person who gave the recipe to the household, e.g. `Aunt Example`. It powers the "From" list on Browse and the provenance line on the recipe. |
| Collection | Where it lives, e.g. `Example Family Cookbook`. |
| Author as published | The author named in the source, such as a cookbook or TV cook. Not the same as "From". |
| Source link | The web address it came from. |
| Categories | See below. |
| Tags | See below. |

## Ingredients

Type **one ingredient per line**, the way you'd write it:

```text
1 8-oz pkg cream cheese, softened
1½ cups sugar
3-4 Tbsp lemon juice
pinch of salt
```

Each line is kept exactly as typed. The app also tries to read the amount
and unit from it so the recipe can be scaled; a line it can't read is kept
as it is and simply isn't scaled. Unicode fractions (`½`, `1½`), ranges
(`3-4`, `1 to 2`), and package sizes (`8-oz`, `(15 oz)`) are understood.
`t` means teaspoon and `T` tablespoon.

When a recipe has parts, use **groups**: fill in **Group name** (e.g.
`Crust`), then tap **+ Ingredient group** for the next part (e.g.
`Filling`). A group with no lines is dropped when you save.

## Steps

Leave a **blank line between steps**. Lines without a blank line between
them stay together as one step:

```text
Preheat the oven to 350 degrees. Line a 9x13 pan with foil.

Mix flour, powdered sugar, butter, and salt until crumbly.
Press firmly into the pan.

Bake 18-20 minutes, until the edges are just golden.
```

That is three steps. Step groups work like ingredient groups: **Group
name**, then **+ Step group**. Steps are numbered straight through all
groups.

Write times the usual way ("Bake 18-20 minutes", "chill 2 hours"): cook
mode turns them into tap-to-start timers. See
[Use cook mode](use-cook-mode.md).

## Categories and tags

**Categories** are hierarchical. Separate levels with `>` and categories
with `;`:

```text
Desserts > Cookies; Holiday
```

This puts the recipe in *Desserts › Cookies* and in *Holiday*. Browsing
*Desserts* includes everything in *Cookies*. New categories are created as
you use them. (`›` works as well as `>`, and a new line works as well as
`;`.)

**Tags** are a comma-separated list: `party, make-ahead`. Duplicates are
removed, ignoring case and accents.

## Photos

Photos are added after the recipe exists:

1. Open the recipe and tap **Edit**.
2. Under **Photos**, tap **Add a photo**, take a picture or choose one, and
   tap **Upload**.

JPEG, PNG, GIF, and WebP are accepted, up to 15 MB. Every photo is
re-encoded: it is resized to at most 2048 pixels on the long edge, a
480-pixel thumbnail is made, and location and other metadata are removed.
The first photo is the main one; use **Make main** to change it, and
**Remove** to take one off.

## Edit a recipe

Open the recipe and tap **Edit**, change what you need, and tap **Save**.

Every save is kept as a **revision**. If two people edit the same recipe
at once, the second to save sees "Someone else saved this recipe while you
were editing", with their text still in the form and a link to open the
current version. Saving again then replaces the other edit on purpose (the
other edit stays in the history).

Editing an imported recipe never touches its original text ("View
original"), its review flags, or other import details that the editor
doesn't show.

## Go back to an earlier version

1. On the recipe, tap **History**.
2. Open a revision to see it, then tap **Restore this version**.

Restoring saves the old content as a new revision, so nothing is lost
either way.

## Delete and restore

- **Move to trash** at the bottom of a recipe removes it. Tap **Undo** in
  the message that follows to bring it straight back.
- **Trash** (in the footer) lists deleted recipes with a **Restore**
  button. Recipes are removed permanently after 30 days (or
  `GRIMOIRE_TRASH_DAYS`).
- Admins can **Empty trash** at once, after ticking a confirmation box.
  This can't be undone.

## Favorites

Tap **☆ Favorite** on a recipe to add it to **Your favorites** on the home
page. Favorites are per person. On a phone with the app installed, your
favorites are also kept for offline reading.
