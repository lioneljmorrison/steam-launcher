// Print templates and settings, shared by the games page and Settings.

// card is the cell on the stock; rotate turns portrait cards to fit
// landscape cells. All sizes are what the stock measures; Avery lists
// 28877 as using the same layout as 5371 (2 × 5, 0.5in top, 0.75in sides).
const TEMPLATES = {
  "avery-28877": { label: "Avery 28877 Clean Edge (10 per sheet)", page: ["8.5in", "11in"], cols: 2, rows: 5,
                   card: ["3.5in", "2in"], margin: "0.5in 0.75in", gap: "0", pad: "3mm", rotate: true },
  "avery-5371":  { label: "Avery 5371 business cards (10 per sheet)", page: ["8.5in", "11in"], cols: 2, rows: 5,
                   card: ["3.5in", "2in"], margin: "0.5in 0.75in", gap: "0", pad: "3mm", rotate: true },
  "cr80":        { label: "Credit card size, 54 × 85.6 mm (9 per sheet)", page: ["8.5in", "11in"], cols: 3, rows: 3,
                   card: ["53.98mm", "85.6mm"], gap: "6mm", center: true, cutlines: true, pad: "3mm" },
  "trading":     { label: "Trading card, 63 × 88 mm (9 per sheet)", page: ["8.5in", "11in"], cols: 3, rows: 3,
                   card: ["63mm", "88mm"], gap: "0", center: true, cutlines: true, pad: "3mm" },
  "toploader":   { label: "Top loader, 2¾ × 3⅞ in (6 per sheet)", page: ["8.5in", "11in"], cols: 2, rows: 3,
                   card: ["3.875in", "2.75in"], gap: "5mm", center: true, cutlines: true, pad: "3mm", rotate: true },
  "labelife":    { label: "LabelLife thermal label, 3 × 2 in", page: ["3in", "2in"], cols: 1, rows: 1,
                   card: ["3in", "2in"], gap: "0", mono: true, pad: "2.5mm", singleSided: true, rotate: true },
};

const SIDES = { ab: "Both sides (A art, B details)", a: "Side A only (art)", b: "Side B only (details)" };

// normalizePrint fills in defaults and drops combinations the template
// can't print: labels are one-sided, so they print side A or side B, not both.
function normalizePrint(p) {
  p = { template: "avery-28877", sides: "ab", showQR: true, rounded: false, oneSheet: false, ...p };
  if (!TEMPLATES[p.template]) p.template = "avery-28877";
  if (!SIDES[p.sides]) p.sides = "ab";
  if (TEMPLATES[p.template].singleSided && p.sides === "ab") p.sides = "b";
  return p;
}

// printSummary describes the settings in a line, for the games page.
function printSummary(p) {
  const parts = [TEMPLATES[p.template].label, SIDES[p.sides]];
  if (p.sides === "ab" && p.oneSheet) parts.push("A and B on one sheet");
  if (p.sides !== "a") parts.push(p.showQR ? "QR code" : "no QR code");
  if (p.rounded) parts.push("rounded corners");
  return parts.join(" · ");
}
