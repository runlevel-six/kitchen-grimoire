# How to use cook mode

Cook mode shows one step at a time in large type, keeps the screen on, and
turns times in the steps into timers, so you can cook from a phone propped
on the counter.

## Start and move through the steps

1. Open a recipe and tap **Start cooking**. (A recipe with no steps has no
   cook mode.)
2. Move between steps with **Next →** and **← Back**, by swiping left or
   right on the step, or with the arrow keys (and Space for next) on a
   keyboard. The top bar shows "Step 3 of 7"; a step that belongs to a
   group, such as *Filling*, shows the group's name above it.
3. On the last step, **Done** takes you back to the recipe. **✕** leaves at
   any time.

The step you're on is in the address (`?step=3`), so reloading or coming
back to the tab keeps your place. Without JavaScript, Back and Next still
work as ordinary links.

## Keep the screen on

When the browser supports the Screen Wake Lock API, the top bar shows
**Screen stays on**, and the phone won't dim or lock while cook mode is
open. If the lock is lost (for example when you switch apps), it is taken
again when you come back.

If it shows **Screen may sleep**, the browser can't keep the screen on.
Raise the phone's auto-lock time while you cook. Wake lock also needs a
secure connection (HTTPS, or `localhost`); on a plain-HTTP development
server reached from a phone it is unavailable.

### On the recipe page

The recipe page doesn't keep the screen on by itself, so browsing doesn't
drain the battery. When you need to read a recipe for a long time without
cook mode, tap **Keep screen on** next to **Favorite**. The button stays
highlighted while the lock is held; tap it again to let the screen sleep.
Leaving the page turns it off, and so does the browser dropping the lock
while the page is showing (for example in low-power mode). Switching apps
and coming back takes the lock again.

The button only appears when the browser supports the Screen Wake Lock
API. On iPhone and iPad that means iOS 16.4 or later in Safari, and
iOS 18.4 or later when the app is opened from the home screen.

## Timers

Times written in a step, such as "Bake 18-20 minutes" or "chill 1½ hours",
appear as buttons. Tap one to start a timer:

- For a range, the timer uses the longer time (20 minutes for "18-20
  minutes").
- Hours, minutes, and seconds are recognized (`hr`, `min`, `sec` too).
  "Overnight" is not a timer.
- Running timers are listed at the bottom with the step and recipe, and
  keep running across steps.
- Timers remember their end time, not a countdown, so they survive the
  screen locking or the page reloading.
- When a timer ends, the phone vibrates (where supported) and, if you
  allowed notifications when asked, shows a "Timer done" notification.
- **✕** cancels a running timer or dismisses a finished one.

## Ingredients while you cook

Tap **Ingredients** to open the drawer with the full list. Tick off
ingredients as you add them. Check-offs are remembered for that recipe in
this browser tab, shared between the recipe page and cook mode; **Clear
checks** resets them.

Cook mode lists the ingredients as written. To cook a doubled batch, pick
**2×** under **Scale** on the recipe page first and keep that page open
for reference: cook mode itself doesn't scale.

## Cooking offline

Once the app is installed and you have opened a recipe, it (and its cook
mode, if opened) is saved on the phone, and the recipe shows **Available
offline**. Your favorites are saved too, when you visit the home page.
The phone keeps the 60 most recently saved pages. If the connection
drops, a banner says "You're offline. Showing saved copies." Editing
needs a connection.
