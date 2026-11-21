// SPDX-License-Identifier: Apache-2.0
// A tiny stand-in recipe website for import tests. Invented content only.
import http from "node:http";

const port = Number(process.argv[2] || 18556);
// 4x4 orange PNG.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAIAAAAmkwkpAAAAEElEQVR4nGO4U6EBRwzEcQB1EhfBz/+4tAAAAABJRU5ErkJggg==",
  "base64",
);

const page = `<!doctype html><html><head><title>Example Skillet Cornbread</title>
<script type="application/ld+json">${JSON.stringify({
  "@context": "https://schema.org",
  "@type": "Recipe",
  name: "Example Skillet Cornbread",
  author: { "@type": "Person", name: "Example Test Kitchen" },
  recipeYield: "8 wedges",
  totalTime: "PT35M",
  image: "/cornbread.png",
  recipeIngredient: ["1 cup cornmeal", "1 cup flour", "1 cup buttermilk", "2 eggs"],
  recipeInstructions: [
    { "@type": "HowToStep", text: "Heat a skillet in the oven." },
    { "@type": "HowToStep", text: "Mix, pour in, and bake 20 minutes." },
  ],
})}</script></head><body><h1>Example Skillet Cornbread</h1></body></html>`;

http
  .createServer((req, res) => {
    if (req.url === "/health") return res.end("ok");
    if (req.url === "/cornbread") {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      return res.end(page);
    }
    if (req.url === "/cornbread.png") {
      res.setHeader("Content-Type", "image/png");
      return res.end(png);
    }
    res.statusCode = 404;
    res.end("not found");
  })
  .listen(port, "127.0.0.1");
