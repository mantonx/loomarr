const tvGuideFrom = Date.UTC(2026, 8, 4, 6, 0, 0);
const tvGuideTo = tvGuideFrom + 2 * 60 * 60_000;
const tvGuideNow = tvGuideFrom + 25 * 60_000;
const tvGuideAt = (minutes: number) => tvGuideFrom + minutes * 60_000;

const channelDefinitions = [
  ["noir", "Noir Nights", 19],
  ["games", "Game Show Vault", 20],
  ["nature", "Nature Documentaries", 21],
  ["horror", "Cozy Autumn Horror", 22],
  ["kung-fu", "Kung Fu Theater", 23],
  ["anime", "Midnight Anime", 24],
] as const;

const tvGuideChannels = channelDefinitions.map(([channelId, name, number]) => ({
  airings: [
    {
      description: "A focused episode description that belongs in the detail card.",
      episode: 4,
      genres: ["Drama", "Adventure"],
      kind: "program" as const,
      rating: "TV-PG",
      scheduleBlockId: `${channelId}-pilot`,
      season: 2,
      series: name,
      startMs: tvGuideAt(0),
      stopMs: tvGuideAt(30),
      title: "Pilot episode",
      year: 1996,
    },
    {
      kind: "program" as const,
      scheduleBlockId: `${channelId}-late`,
      startMs: tvGuideAt(30),
      stopMs: tvGuideAt(65),
      title: channelId === "nature" ? "Blue Planet — The Deep" : `${name} Late`,
    },
    {
      kind: "program" as const,
      scheduleBlockId: `${channelId}-after-hours`,
      startMs: tvGuideAt(65),
      stopMs: tvGuideAt(120),
      title: "After Hours",
    },
  ],
  channelId,
  name,
  number,
  pendingCount: 0,
  status: "live" as const,
}));

export { tvGuideChannels, tvGuideFrom, tvGuideNow, tvGuideTo };
