// Prepare only the isolated anonymous executable. Formal Go source is untouched.
// This overlay is only for the isolated tag catalog and real tag image services.
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import path from "node:path";
const root = path.resolve(import.meta.dirname, "../.."),
  source = path.join(root, "cmd/fixtures/server.go"),
  adapter = path.join(root, ".runtime/tests/tag-fixture-adapter");
const original = await readFile(source, "utf8");
const args = [
  "build",
  "-trimpath",
  "-o",
  path.join(root, ".runtime/bin/fixtures"),
];
let adapted = original;
const dependency = '"nyamangaserver.local/server/internal/httpapi"';
function needImport(name) {
  const pkg = `"nyamangaserver.local/server/internal/${name}"`;
  if (!adapted.includes(pkg))
    adapted = adapted.replace(dependency, `${dependency}\n${pkg}`);
}
if (!adapted.includes(".WithTagCatalog(")) {
  const anchor = ".WithMetadata(meta, ctx)";
  if (adapted.split(anchor).length !== 2)
    throw Error("Review anonymous tag catalog assembly before adapting it");
  needImport("tagcatalog");
  adapted = adapted.replace(
    anchor,
    ".WithTagCatalog(tagcatalog.New(db))" + anchor,
  );
}
if (!adapted.includes(".WithTagImages(")) {
  const assembly =
      "maint := maintenance.New(db, c, cat, fixtureDrain{meta, recommend}, logger)",
    route = ".WithMetadata(meta, ctx)";
  if (adapted.split(assembly).length !== 2 || adapted.split(route).length !== 2)
    throw Error("Review anonymous tag image assembly before adapting it");
  needImport("tagimages");
  adapted = adapted
    .replace(
      assembly,
      "tagPictures := tagimages.New(db, c, cat.Pool)\n\t" +
        assembly +
        ".WithTagImages(tagPictures)",
    )
    .replace(route, ".WithTagImages(tagPictures)" + route);
}
if (!adapted.includes(".WithEnhancements(")) {
  needImport("enhancements");
  needImport("model");
  adapted = adapted.replace(
    "delay := 300 * time.Millisecond",
    'delay := 300 * time.Millisecond\n\tif os.Getenv("NYAMANGA_FIXTURE_ENHANCEMENT_SLOW") == "1" { delay = 2 * time.Second }',
  );
  const anchor = "\thandler := httpapi.New";
  if (adapted.split(anchor).length !== 2)
    throw Error("Review anonymous enhancement assembly");
  adapted = adapted
    .replace(
      anchor,
      "\toptional := enhancements.New(db, fixtureEnhancementRuntime{recommend, meta, db})\n\toptional.Start(ctx)\n" +
        anchor,
    )
    .replace(
      ".WithMetadata(meta, ctx)",
      ".WithEnhancements(optional, db).WithMetadata(meta, ctx)",
    )
    .replace(
      "errors.Join(server.Shutdown(shutdown),",
      "errors.Join(server.Shutdown(shutdown), optional.Shutdown(shutdown),",
    );
  adapted += `
// Only the isolated fixture executable uses this fixed, offline runtime.
type fixtureEnhancementRuntime struct { rec *recommendation.Service; meta *metadata.Service; db *store.Store }
func (r fixtureEnhancementRuntime) DrainEnhancement(ctx context.Context, feature string) error {
 if feature == model.EnhancementDictionary { return r.meta.DrainDictionary(ctx) }
 return r.rec.DrainFeature(ctx, feature)
}
func (r fixtureEnhancementRuntime) RestoreDictionary(ctx context.Context) error { return r.meta.RestoreDictionary(ctx) }
func (r fixtureEnhancementRuntime) EnhancementResources(ctx context.Context) map[string]string {
 capability := r.rec.Capabilities(ctx)
 state, _ := capability["resource_state"].(string)
 if state == "" { state = "unverified" }
 dictionary, err := r.db.DictionaryResourceState(ctx)
 if err != nil { dictionary = "unavailable" }
 return map[string]string{model.EnhancementWork:state,model.EnhancementSeries:state,model.EnhancementDictionary:dictionary}
}
`;
}
if (!adapted.includes(".WithGalleryHighlight(")) {
  needImport("galleryhighlight");
  adapted = adapted.replace(
    ".WithMetadata(meta, ctx)",
    ".WithGalleryHighlight(galleryhighlight.New(db)).WithMetadata(meta, ctx)",
  );
}
if (!adapted.includes(".WithHealth(")) {
  needImport("model");
  const anchor = "\tserver := &http.Server";
  if (adapted.split(anchor).length !== 2)
    throw Error("Review anonymous health assembly");
  adapted = adapted
    .replace(
      anchor,
      `
  health := maintenance.NewHealth(db,c,cat.Pool)
  if os.Getenv("NYAMANGA_FIXTURE_HEALTH_DELAY")=="1" {
    health.BeforeIO=func(phase string)error {if phase=="snapshot" {time.Sleep(2500*time.Millisecond)};return nil}
    health.BeforeObject=func(ctx context.Context,_ model.HealthObject){select{case <-ctx.Done():case <-time.After(10*time.Millisecond):}}
  }
  health.CoverBytes=cat.HealthCoverBytes
  health.TagBytes=tagPictures.HealthCacheBytes
  health.DictionaryBytes=meta.HealthDictionary
  handler.WithHealth(health)
  health.Drain=func(ctx context.Context) error {return errors.Join(handler.DrainHealthHTTP(ctx),maint.Drain(ctx),cat.Drain(ctx),meta.Drain(ctx),meta.DrainDictionary(ctx),recommend.Drain(ctx))}
  health.Start(ctx)
` + anchor,
    )
    .replace(
      "errors.Join(server.Shutdown(shutdown),",
      "errors.Join(server.Shutdown(shutdown),health.Shutdown(shutdown),",
    );
}
if (adapted !== original) {
  await mkdir(adapter, { recursive: true, mode: 0o700 });
  await writeFile(path.join(adapter, "server.go"), adapted, { mode: 0o600 });
  const overlay = path.join(adapter, "overlay.json");
  await writeFile(
    overlay,
    JSON.stringify({ Replace: { [source]: path.join(adapter, "server.go") } }),
    { mode: 0o600 },
  );
  args.splice(1, 0, "-overlay", overlay);
}
const result = spawnSync("go", [...args, "./cmd/fixtures"], {
  cwd: root,
  stdio: "inherit",
  env: {
    ...process.env,
    GOTOOLCHAIN: "local",
    GOPROXY: "off",
    GOSUMDB: "off",
    GOPATH: path.join(root, ".runtime/cache/go"),
    GOCACHE: path.join(root, ".runtime/cache/go-build"),
  },
});
if (result.error) throw result.error;
if (result.status !== 0)
  throw Error(`Anonymous fixture build failed (${result.status})`);
