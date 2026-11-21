# Your first run

In this tutorial you'll run Kitchen Grimoire on your own computer, look
around the two sample recipes, add a recipe of your own, scale it, cook it
step by step, and find it again with search. At the end you'll open it on
your phone and put it on the home screen. It takes about 20 minutes.

This is a development setup: there's no sign-in, and everyone who can
reach it is the same user, `dev`. Don't put it on the internet. To run it
for real, follow [Deploy on Kubernetes](deploy-on-kubernetes.md)
afterwards.

## What you need

- [Go](https://go.dev/dl/) 1.27 or newer (the build fetches the exact
  toolchain it needs);
- `git` and `make`;
- a browser, and optionally a phone on the same Wi-Fi network.

## 1. Start the server

```sh
git clone https://github.com/runlevel-six/kitchen-grimoire.git
cd kitchen-grimoire
make dev
```

The first run downloads Go modules and the template generator, so it takes
a minute. Then the server starts and logs JSON lines, including:

```text
{"level":"WARN","msg":"AUTHENTICATION DISABLED: every request is treated as user 'dev'. Never use this outside development."}
{"level":"INFO","msg":"applied migration","version":1,"name":"init"}
{"level":"INFO","msg":"seeded sample recipes (dev mode, empty database)"}
```

`make dev` turns authentication off (`GRIMOIRE_AUTH_MODE=none`, which is
only allowed with `GRIMOIRE_DEV=1`), listens on port 8080 on every network
interface, and keeps its data in `local/data/` inside the clone (ignored by
git). Because the database was empty, it added two invented sample
recipes. Leave it running; press Ctrl-C to stop it later.

The admin port (metrics and backups, no authentication) also listens, on
`127.0.0.1:9090`, so only this computer can reach it. The app port is
reachable from your local network, which is what lets you try it on a
phone in step 7; do this on a network you trust.

## 2. Look around

Open <http://localhost:8080>.

The home page shows a search box and **Recently added**, with the two
samples. Open **Example Lemon Cream Bars**:

- Under the title, "From Aunt Example · Example Family Cookbook" says who
  the recipe came from.
- The ingredients are in two groups, *Crust* and *Filling*, with a
  checkbox for each line.
- At the bottom, **View original** shows the recipe as it was first
  written, before it was tidied up. Kitchen Grimoire never throws that
  away.

## 3. Add a recipe

Tap **+ New** at the top and fill in:

- **Title**: `Example Pancakes`
- **Makes**: `8 pancakes`, and **Servings**: `8`
- **Ingredients**, one per line:

  ```text
  1½ cups flour
  1 Tbsp sugar
  2 tsp baking powder
  ½ tsp salt
  1¼ cups milk
  1 egg
  3 Tbsp butter, melted
  a little oil for the pan
  ```

- **Steps**, with a blank line between steps:

  ```text
  Whisk the flour, sugar, baking powder, and salt.

  Add the milk, egg, and butter and stir until just combined. Rest 5 minutes.

  Heat an oiled pan over medium heat. Cook ¼ cup of batter per pancake,
  2-3 minutes a side.
  ```

- **From (who gave it to us)**: `Grandpa Example`
- **Categories**: `Breakfast > Pancakes`
- **Tags**: `weekend, kids`

Tap **Save**. The recipe opens with "Recipe added." The two lines of the
last step stayed together, because only a blank line starts a new step.

## 4. Scale it

Under **Ingredients**, next to **Scale**, tap **2×**. The amounts double
(`3 cups flour`, `2½ cups milk`) and the yield says "Makes 16 (2× 8
pancakes)". The last line, `a little oil for the pan`, has no amount the
app could read, so it is shown as written with a **not scaled** marker.
Nothing is changed in the recipe itself; scaling only affects what you
see. Tap **1×** to go back.

## 5. Cook it

Tap **Start cooking**. Cook mode shows one step at a time in large type:

1. Tap **Next →**, or swipe left, to move on. "Step 2 of 3" shows where
   you are.
2. On step 2, "5 minutes" is a button. Tap it: a timer starts at the bottom
   of the screen. Your browser may ask to show notifications; allow it if
   you'd like an alert when the timer ends.
3. Tap **Ingredients** to open the list, and tick off a couple of lines.
4. On the last step, tap **Done**.

On a computer the top bar may say **Screen stays on**: cook mode asks the
browser to keep the screen from dimming. Back on the recipe page, the lines
you ticked are still ticked. More in [Use cook mode](../how-to/use-cook-mode.md).

## 6. Find it again

Go home (tap the logo) and try these searches:

| Search | Finds |
|---|---|
| `pancakes` | Your recipe |
| `pan` | Your recipe and the lemon bars, which mention a pan: the last word also matches as a prefix |
| `grandpa` | Your recipe, by who it came from |
| `breakfast` | Your recipe, by category |
| `pan -pancakes` | The lemon bars only: `-` excludes a word |

Then tap **Browse**: your recipe is under **Breakfast › Pancakes**, under
**From** as *Grandpa Example*, and under the tags `kids` and `weekend`. Tap
**Breakfast**, then remove the filter with the **✕** on its chip. See
[Find recipes](../how-to/find-recipes.md) for the full syntax.

## 7. Open it on your phone

Find your computer's address on the local network (on macOS:
`ipconfig getifaddr en0`; on Linux: `hostname -I`). On a phone on the same
Wi-Fi, open `http://<that address>:8080`. If it doesn't load, your
computer's firewall is probably blocking port 8080.

The layout is built for phones: big tap targets, one column, and the
ingredients beside the steps only on wide screens.

## 8. Add it to your home screen

Kitchen Grimoire is a Progressive Web App, so it can sit on your home
screen like an app and open without the browser's address bar:

- **iPhone or iPad (Safari):** tap the **Share** button, then **Add to Home
  Screen**, then **Add**.
- **Android (Chrome):** open the **⋮** menu and tap **Add to Home screen**
  (or **Install app**).

The icon is the Grimoire book, named "Grimoire".

Browsers only enable the offline parts of a PWA (the service worker, which
keeps recipes you've opened readable without a connection) and the screen
wake lock on secure pages: HTTPS, or `localhost`. On your phone, over plain
`http://` to your computer, the home-screen icon works but offline reading
and "Screen stays on" don't. On a real deployment behind HTTPS they do.

## 9. Clean up

Press Ctrl-C in the terminal to stop the server. Your recipe is still in
`local/data/`, and `make dev` picks it up next time (it only adds the
samples to an empty database). To start fresh, delete `local/data/`.

## Next steps

- [Deploy on Kubernetes](deploy-on-kubernetes.md) behind single sign-on, for
  your household.
- [Import a recipe from a link](../how-to/import-a-recipe-from-a-link.md).
- [How Kitchen Grimoire is built](../explanation/architecture.md).
