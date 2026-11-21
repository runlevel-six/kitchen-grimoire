# Capture recipes from your browser or phone

Some recipe sites refuse requests from servers, so pasting their link into **Import** fails with "That site refused our request". Capture sends the page from your own browser instead. Your browser already has the page, so the site's blocking doesn't matter.

Both methods end at the same place: the **Check and save** page with the recipe filled in. Nothing is saved until you press **Save**. The page's HTML is never stored; only the extracted recipe data is kept, as `raw_source`.

## On a computer: the bookmarklet

1. Open **Settings** in Kitchen Grimoire.
2. Drag the **Save to Kitchen Grimoire** button to your browser's bookmarks bar.
3. On a recipe page, click the bookmark. A Kitchen Grimoire window opens with the recipe ready to check.

If nothing arrives after a few seconds, the window says so. Two things cause this:

- **Pop-ups are blocked.** Allow pop-ups for the recipe site and try again.
- **The site cuts the link between windows**, with a `Cross-Origin-Opener-Policy` header. Use the phone method, or type the recipe in.

The capture page only accepts a page whose message really comes from the address it claims, so one site can't pass itself off as another.

## On an iPhone or iPad: the share sheet

### 1. Create a capture token

In **Settings → Capture tokens**, give the token a name (for example "My iPhone") and press **Create token**. Copy it right away: it's shown only once. Kitchen Grimoire stores only a hash of it.

A capture token can do exactly one thing: send pages to `/api/v1/capture` for you. You can revoke it from the same page at any time. Admins can see and revoke everyone's tokens.

### 2. Build the shortcut

In the **Shortcuts** app, create a new shortcut:

1. In the shortcut's details, turn on **Show in Share Sheet** and accept **Safari web pages**.
2. Add **Run JavaScript on Web Page** with:

   ```js
   completion({url: document.URL, html: document.documentElement.outerHTML});
   ```

3. Add **Get Contents of URL**:
   - **URL:** `https://<your Kitchen Grimoire address>/api/v1/capture` (Settings shows the exact address)
   - **Method:** `POST`
   - **Headers:** `Authorization` = `Bearer <your token>`
   - **Request body:** JSON, with `url` and `html` set from the previous action's result
4. Add **Get Dictionary Value** for the key `url`.
5. Add **Open URLs**.

### 3. Use it

In Safari, open a recipe, tap **Share**, then choose your shortcut. Safari opens the **Check and save** page. You need to be signed in to Kitchen Grimoire in Safari; the link is yours alone and expires after an hour.

Captures are limited to 30 an hour per person.

## For the operator: let the capture endpoint past SSO

The phone posts to `/api/v1/capture` with its token, not with an SSO session cookie, so the SSO proxy must let **that one path** through without a login. Kitchen Grimoire checks the token itself.

With Authelia, add the path to the app's bypass rule, keeping it path-limited:

```yaml
access_control:
  rules:
    - domain: 'recipes.example.com'
      resources:
        - '^/(healthz|readyz)$'
        - '^/api/v1/capture$'
      policy: 'bypass'
    - domain: 'recipes.example.com'
      policy: 'one_factor'
      subject: ['group:family', 'group:admins']
```

Everything else, including the draft link the phone opens, stays behind SSO.

## Troubleshooting

| What you see | Why | What to do |
|---|---|---|
| `invalid or revoked token` | The token is wrong or was revoked | Create a new token and update the shortcut |
| The phone opens the SSO login page | The SSO session in Safari expired | Sign in, then share again |
| `That capture has expired or isn't yours` | More than an hour passed, the server restarted, or you're signed in as someone else | Share the page again |
| `No recipe found on that page` | The page has no structured recipe data | Use **New** and type it in |
| `too many captures` | More than 30 in an hour | Wait a while |
