# Community Groups Platform — Visual Design

The source of truth for color, type, spacing, components, layouts and the four looks. Tailwind classes in `development-plan.md` are illustrative; the values here win.

**Companion documents:** `prd.md` (requirements), `development-plan.md` (implementation, "Looks" and Phase 4b), `workflows.md` (screen flows), `decisions.md`. **Visual reference:** `mockup.html` (open it in a browser; it shows all four looks with a brand-color switch, light and dark, and a working RSVP form). Where the mockup and this file disagree, this file wins.

---

## 1. Principles

1. **Minimal interaction first.** The RSVP form is the most important component on the platform: email, optional name, one button. Nothing competes with it on a meeting page.
2. **Phone first.** Design at 360 px wide and enhance upward. Most members arrive from a link in a message or an email.
3. **Clean is the base.** Every group, and every platform page, starts with the Clean look. The other looks restyle it; they never replace its structure for forms, the feed or admin.
4. **One brand color.** A paid group picks one color. The app derives everything else from it. A free group uses the platform default.
5. **Readable before pretty.** Text contrast is computed, not hoped for. Motion is decoration and can be turned off without losing any information.
6. **White-label means no platform marks** on a group's pages: no platform name, logo or link (see `prd.md` §4.3).

---

## 2. Tokens

All tokens are Tailwind v4 `@theme` variables in `input.css`, so they are real CSS custom properties. Values below are the platform defaults. A group overrides only the ones marked **group**.

### 2.1 Color

**Neutrals (Clean)**

| Token | Light | Dark | Use |
|---|---|---|---|
| `--color-ground` | `#f5f7fa` | `#0f141c` | Page background |
| `--color-surface` | `#ffffff` | `#171e2a` | Cards, inputs, nav |
| `--color-ink` | `#172033` | `#e8edf5` | Body text, headings |
| `--color-muted` | `#5d6779` | `#98a3b6` | Secondary text, captions |
| `--color-line` | `#e3e8ef` | `#263041` | Borders, dividers |

Neutrals lean slightly blue so they sit well beside the default brand color. Muted text on ground and on surface must stay at 4.5:1 or better (checked in `palette_test.go`).

**Brand and derived (group)**

| Token | Source | Use |
|---|---|---|
| `--color-brand` | The group's hex, or the default `#2563eb` | Primary buttons, brand bars, logo tile, focus ring |
| `--color-brand-contrast` | White or `#14171f` (see 4.2) | Text and icons on top of brand |
| `--color-brand-text` | Brand adjusted for text (see 4.3) | Links and brand-colored text on ground and surface |
| `--color-accent-1`, `-2`, `-3` | Derived (see 4.1) | Avatars, tiles, stickers, marquee. Used by Lively, Confetti and Poster only |
| `--color-tint-1` … `-4` | Derived (see 4.1) | Soft backgrounds for pills, date tiles, info tiles |

Light and dark values are emitted for every derived token (tints are dark and low-chroma in dark mode).

**Semantic colors** (never the brand color, and not overridden by groups)

| Meaning | Text | Background | Dark text | Dark background |
|---|---|---|---|---|
| Success | `#166534` | `#dcfce7` | `#4ade80` | `#123222` |
| Warning | `#92400e` | `#fef3c7` | `#fbbf24` | `#3a2a08` |
| Danger | `#b91c1c` | `#fee2e2` | `#f87171` | `#3b1414` |
| Info | `--color-brand-text` | `--color-tint-1` | same | same |

State is never color alone: success, warning and danger always come with an icon or a word ("Canceled", "Full").

### 2.2 Dark mode

Dark mode follows the visitor's system setting (`prefers-color-scheme`). There is no in-app toggle in v1. Every token has a dark value; component CSS uses tokens only, never literal colors. `color-scheme: dark` is set so native controls follow. The brand color itself does not change in dark mode, but brand-colored text uses `--color-brand-text`, which is lightened.

### 2.3 Typography

| Role | Face | Weights | Notes |
|---|---|---|---|
| All looks: headings and text | **Figtree** | 400, 500, 600, 700, 800 | Variable font |
| Confetti: headings, numbers | **Fredoka** | 500, 600, 700 | Loaded only for groups using Confetti |
| Poster: date, title, buttons | **Unbounded** | 600, 700, 800 | Loaded only for groups using Poster |

Fonts are self-hosted WOFF2 files, served from the visitor's own hostname with a fingerprinted URL and `font-display: swap`. Fallback stack: `ui-sans-serif, system-ui, sans-serif`.

| Step | Size / line height | Use |
|---|---|---|
| `xs` | 12 / 16 | Labels (uppercase, +0.06em tracking), fine print |
| `sm` | 14 / 20 | Captions, metadata, secondary buttons |
| `base` | 16 / 24 | Body text, inputs (16 px minimum so iOS does not zoom) |
| `lg` | 18 / 26 | Lead text, card titles |
| `xl` | 20 / 26 | Meeting title in a card |
| `2xl` | 26 / 30 | Group name, page titles on phone |
| `3xl` | 32 / 36 | Page titles from 640 px up |

Headings use `text-wrap: balance`, weight 700, and `letter-spacing: -0.015em`. Running text is capped at about 65 characters. Digits that line up (counts, times) use `font-variant-numeric: tabular-nums`.

### 2.4 Space, shape, elevation

- **Spacing:** 4 px base: 4, 8, 12, 16, 20, 24, 32, 48. Lay out groups of siblings with flex or grid `gap`, not margins.
- **Gutter:** 16 px on phones, 24 px from 640 px up. Content column max width 56 rem (896 px).
- **Radius:** `sm` 8 (small buttons, chips), `md` 10 (inputs, buttons), `lg` 14 (cards), `full` (pills, avatars).
- **Elevation:** one card shadow, `0 1px 2px rgb(23 32 51 / .06), 0 8px 24px rgb(23 32 51 / .06)` in light; a 1 px line and no shadow in dark.
- **Touch targets:** at least 44 × 44 px for anything tappable, including text links in lists.
- **Breakpoints:** 640 (`sm`), 1024 (`lg`). Layouts are single column below 640.

### 2.5 Motion

Default Clean motion is minimal: a 150 ms color/transform transition on buttons and a 200 ms fade when an HTMX fragment replaces content. The livelier looks add more (section 5). **Everything animated is disabled under `prefers-reduced-motion: reduce`**, and no state change depends on an animation finishing.

---

## 3. Components (Clean)

Each component lists its parts and states. Look-specific differences are in section 5.

**Button**
- Primary: `--color-brand` background, `--color-brand-contrast` text, 10 px radius, 44 px min height, weight 700. States: hover (darken 6%), active (darken 10%), focus (3 px brand ring with 2 px offset), disabled (50% opacity, no pointer), loading (label stays, spinner replaces icon, `aria-busy`).
- Secondary: surface background, 1 px line border, ink text.
- Quiet: no background, `--color-brand-text` text, underline on hover.
- Danger: danger text on danger background; used only for destructive confirms.

**Input**
- 44 px min height, 10 px radius, 1 px line border, surface background, 16 px text. Label above in `sm`/600. Optional fields say "(optional)" in the label, never in the placeholder alone.
- Error: danger border plus a message below with an icon. The message states what to fix ("Enter an email like name@example.com").
- Focus: brand ring, same as buttons.

**Card:** surface, 1 px line border, 14 px radius, card shadow, 16 px padding. Used for the meeting block and for list items that need separation. Not used for every block; plain sections use whitespace.

**Pill / badge:** `xs`/700, full radius, tint background with `--color-brand-text` text. Variants: neutral (chip), semantic (success, warning, danger). Examples: "Next meeting", "Canceled", "Member", "Co-organizer", "Full".

**Navigation (cacheable shell + `/fragments/nav`):** a 56 px bar. Left: group logo tile and name (custom domain) or platform name. Right: "Log in", or the account menu once the nav fragment loads. No platform name or link on custom domains.

**Date tile:** a 56 px rounded tile with the day number (800) and month abbreviation (700, `xs`, uppercase). Appears in Lively, Confetti (as a sticker) and Poster (as a huge numeral).

**RSVP block (fragment)** — the key component. Three states, same footprint so the page does not jump:
1. *Form:* email (hidden when logged in), name (optional, hidden when logged in), one primary button "RSVP". Under it, "{n} going".
2. *Going:* a success tint block with "You're going", the virtual link (if virtual and set), and a quiet "Can't make it" button. The count updates through `HX-Trigger: rsvp-changed`.
3. *Closed:* plain text, "This meeting has already happened" or "This meeting has been canceled". No form.
Errors appear inline under the field. A duplicate RSVP is shown as the *Going* state, with no error.

**Feed post / reply / comment:** author display name (or "Member"), relative time, text, attachments (images inline at 100% width with 10 px radius, documents as a row with file-type icon, name and size). Delete is a quiet button visible to authors and organizers, with a confirm step in the page, not a browser dialog. Replies are indented 16 px, one level only. The composer is a textarea with an "Attach" button and a primary "Post" button.

**Member row (admin):** name, email (organizers only), role pill, joined date, actions menu. Destructive actions need a confirm step.

**Empty states:** one sentence saying what will appear here and one primary action ("No meetings yet. Schedule the first one."). No illustrations in v1.

**Alerts and toasts:** inline alert for page-level messages (semantic tint, icon, text). A toast ("Saved", "Attendance saved") appears for 3 s at the bottom on phones, is announced with `role="status"`, and never holds an action.

**Map block:** Maps Embed iframe, 14 px radius, 208 px high (phone), with the address text above it. If the address has no Place ID, text only.

**Avatar:** 34 px circle with initials (two letters) on an accent color; "+N" for overflow. No photos in v1.

**Confirm step:** a panel that replaces the button row ("Delete this post? [Delete] [Keep]"), not a modal. Typing the group name is required only for deleting a group or transferring ownership.

---

## 4. Palette rules (`internal/branding/palette.go`)

The server computes every color from the validated hex and writes explicit hex values into the page. No browser feature is required. Work in OKLCH.

### 4.1 Derived colors

Let the brand be `(L, C, h)`. Outputs are clipped into sRGB by reducing chroma, not lightness.

| Token | Light mode | Dark mode |
|---|---|---|
| `accent-1` | `L 0.76, C 0.14, h + 150` | same |
| `accent-2` | `L 0.82, C 0.14, h + 60` | same |
| `accent-3` | `L 0.74, C 0.15, h + 270` | same |
| `tint-1` | `L 0.95, C 0.04, h` | `L 0.30, C 0.06, h` |
| `tint-2` | `L 0.95, C 0.045, h + 150` | `L 0.30, C 0.05, h + 150` |
| `tint-3` | `L 0.96, C 0.05, h + 60` | `L 0.30, C 0.05, h + 60` |
| `tint-4` | `L 0.95, C 0.045, h + 270` | `L 0.30, C 0.05, h + 270` |

Text on accents is always `#14171f`. Pairing: `tint-2` goes with `accent-1`, `tint-3` with `accent-2`, `tint-4` with `accent-3`. A brand with chroma under 0.03 (a gray) uses fixed fallback hues (teal, amber, rose) for the accents, so a gray brand still gets a palette.

### 4.2 Text on brand

Compute the contrast of white and of `#14171f` against the brand. Use white if its contrast is at least 4.5:1, otherwise whichever has the higher contrast. If the best available contrast is below 3:1, the settings page warns and suggests the nearest color that passes. (Example: default blue gets white at 5.2:1; marigold `#f59e0b` gets dark text.)

### 4.3 Brand as text

Brand-colored links and labels use `--color-brand-text`, not the brand itself. In light mode lower the brand's OKLCH lightness until it reaches 4.5:1 against surface; in dark mode raise it until it reaches 4.5:1 against the dark surface. The hue is unchanged.

### 4.4 Tests

`palette_test.go` asserts, for brand colors that include pure grays, near-black, near-white, saturated yellow, saturated blue and the default: every text/background pair emitted meets its target (4.5:1 for text, 3:1 for large text and UI borders), and every output is a valid sRGB hex.

---

## 5. The four looks

The look is `groups.look` and sets `data-look` on `<body>`. Clean is the unscoped base; the other three override it. The mockup (`mockup.html`) is the visual reference for the meeting page in each look.

| | **Clean** (default) | **Lively** | **Confetti** | **Poster** |
|---|---|---|---|---|
| Idea | Calm, familiar event page | Clean plus color and a little motion | Playful stickers and tiles | One bold flyer |
| Colors used | Brand + neutrals | Adds accents and tints | Adds four tints + accents | Brand flood + accent strip |
| Fonts | Figtree | Figtree | Fredoka + Figtree | Unbounded + Figtree |
| Corners | 10–14 px | 10–14 px | 16–26 px | 10 px |
| Group header | Name, topic, city, member count | Soft color cover behind the nav, name below | Pill with group name, sticker date | Brand-colored hero with the date numeral |
| Meeting header | Title, time, place in a card | Date tile beside the title; tinted icon chips | Sticker date, tiles for When / Where / Going | Giant day numeral, title, time on the brand flood, then a scrolling facts strip |
| RSVP (form) | Stacked fields, primary button | Same, with a brand-gradient button | Brand panel, white inputs, "Count me in" | Outlined fields, full-width ink button with arrow |
| RSVP (going) | Tint block, "You're going" | Avatar row pops in | "You're in!" panel with confetti burst | Ticket stub with tear line and count |
| Motion | Minimal | Header glow drifts, count ticks, avatars pop | Sticker drops in and wiggles, button squishes, confetti | Strip scrolls, ticket drops in |
| Extra assets | None | None | `confetti.js` (about 1 KB) | None |

Rules for all looks:

1. **Same structure, same fragments.** A look can restyle and can replace three partials (`meeting_header`, `rsvp_going`, `group_header`); it cannot move the RSVP form, change what is cached, or change what the fragments contain.
2. **Admin always uses Clean**, including the look picker's previews.
3. **Emails always use one layout** and the brand color only (section 7).
4. **Long content.** Every look handles a 60-character meeting title, a group name with no spaces, and a 5-line description without clipping. Poster steps the title size down above about 40 characters. Long unbroken strings wrap with `overflow-wrap: anywhere`.
5. **Contrast.** Brand-flood areas (Poster hero, Confetti RSVP panel, buttons) use `--color-brand-contrast`; accent and tint areas use ink text. Never place brand text directly on an accent.
6. **Dark mode.** Poster's hero stays brand-colored in dark mode; the sheet below it and all tints use dark values. Confetti's ground is the dark neutral with a faint brand tint.
7. **Motion budget.** At most one continuous animation visible at a time (header glow or strip scroll). Both stop under reduced motion, and the strip is `aria-hidden`.
8. **Color-blind safety.** Tile color never carries meaning on its own; every tile has a text label.

### Look picker (owner settings)

Four cards in a 2 × 2 grid on phones and a row on desktop. Each card is a small, non-interactive preview of the meeting header in that look, rendered in the group's current color, with the look's name and a one-line description. The selected card has a brand ring and a check. Choosing a card and pressing "Save look" applies it. Free groups see the color and logo fields below it locked, with an "Upgrade" link.

---

## 6. Page layouts

All pages share: nav, a main column (max 56 rem), and a footer with Terms and Privacy (plus the group name on custom domains).

| Page | Layout (phone → wide) |
|---|---|
| **Landing (platform)** | Headline, search box, "Groups near you" cards (name, topic, city, distance, members). Two columns of cards from 640 px. |
| **Directory** | Search, topic filter chips, list of group cards, pagination. City prompt appears inline when location is unavailable. |
| **Group page** | Group header (per look), "Next meeting" card with RSVP, upcoming meetings list, feed (composer if allowed, posts), join button / member badge in the header. |
| **Meeting page** | Meeting header (per look), description, RSVP block, map, discussion (comments). |
| **Login / verify** | Centered single card: one field and one button. The verify page has one button, "Log in as {email}". Always Clean, never cached. |
| **Account / notification settings** | Single column forms; four switches, each saved immediately with a "Saved" note. |
| **Admin dashboard** | Group cards (owner / co-organizer / member), "Create group" primary button. On a custom domain, that group only. |
| **Admin: meetings, members, attendance, analytics** | Single column lists on phone; analytics tables scroll horizontally inside their own container. Attendance is a checklist with a 44 px row and a "Select all" helper. |
| **Owner settings** | Sections in order: Look, Plan, Branding, Custom domain, Danger zone. |
| **Error / offline** | One message, one way out ("Go back", "Try again"). Unbranded on custom domains. |

**Cacheable shell vs fragments** (see `development-plan.md`): every page's static structure is in the cached shell; nav, RSVP, join, feed and comments are placeholders that show a neutral skeleton line ("Loading…") until the fragment arrives, and keep their height to avoid layout shift.

---

## 7. Email

- One layout for every look: 560 px column, white card on a light gray ground, group logo or name at the top (no platform name on white-label groups), the content, a primary button in the brand color with `--color-brand-contrast` text, then a footer with the unsubscribe link and the group name.
- System font stack only (no web fonts). Plain-text part is always included.
- Time is shown in the meeting's timezone with the abbreviation. Virtual links appear only for recipients who RSVP'd.
- Auth emails (magic link, invites) are plainer: no group-authored content, one button, one line about expiry (15 minutes).
- Emails do not use derived accents or tints.

## 8. Icons, imagery, PWA

- **Icons:** one inline SVG set, 24 px grid, 1.8 px stroke, `currentColor`. Needed: clock, pin, calendar, people, link, paperclip, check, close, menu, plus, warning.
- **Images:** cover images are 16:9, uploaded images keep their aspect ratio, `loading="lazy"`, always with alt text from the filename or "Image". Logos render in a 28–40 px rounded tile.
- **PWA:** icons at 192 and 512 px (maskable). On a custom domain the group's logo (or a brand-colored tile with initials) is the icon. `theme-color` is the brand color. The look does not affect the manifest.

## 9. Accessibility checklist

- Text contrast 4.5:1 (3:1 for large text and UI borders), computed by `palette.go` for every emitted pair.
- Visible focus ring on every interactive element (3 px, brand-colored, 2 px offset).
- 44 px touch targets; forms usable with the on-screen keyboard open.
- Every input has a label; errors are tied to inputs with `aria-describedby`; fragment updates use `aria-live="polite"` (RSVP state, toasts).
- Dates and times use `<time datetime>`; the time zone abbreviation is always shown.
- Decorative elements (glow, sticker, strip, confetti, avatars in the going state) are `aria-hidden`.
- Reduced motion respected; no content appears only through animation.
- Pages work with JavaScript off for reading; HTMX-driven forms degrade to normal form posts where practical.

## 10. Acceptance for Phase 0 and Phase 4b

- **Phase 0:** `input.css` holds the Clean tokens from section 2 (light and dark), the type scale and the Figtree font files; the base layout renders at 360, 768 and 1024 px in both color modes.
- **Phase 1:** Clean components from section 3 exist as template partials.
- **Phase 4b:** `palette.go` meets section 4; each look matches `mockup.html` at 360 px in light and dark with the default color and with three other test colors (a gray, marigold, a dark purple); the checks in section 5 and the reduced-motion behavior pass.
