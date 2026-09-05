import { createServer } from "node:http";

const port = Number.parseInt(process.argv[2] ?? "18778", 10);
if (!Number.isInteger(port) || port < 1 || port > 65_535) {
  console.error("usage: node tv-native-reference-server.mjs PORT");
  process.exit(2);
}

let storyId = "";
const server = createServer((request, response) => {
  if (request.method === "GET") {
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ storyId }));
    return;
  }
  if (request.method === "PUT") {
    const chunks = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => {
      storyId = Buffer.concat(chunks).toString("utf8");
      response.writeHead(204);
      response.end();
    });
    return;
  }
  response.writeHead(405);
  response.end();
});

server.listen(port, "127.0.0.1", () => {
  console.log(`tv-native-reference-server: listening on http://127.0.0.1:${port}`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
