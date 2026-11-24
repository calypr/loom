export const title = "Source load to live project release";
export const state = (v) => ({
  "Source load": ({uploading: "Uploading files", ready: "Copied, not published",
    pending: v.worker === "none" ? "Publishing, no worker" : "Publishing",
    published: "Published", superseded: "Superseded"})[v.load],
  "Data": v.manifest === "loading" ? "Rows loading" :
    ({none: "Rows staged", running: "Dataframe building", validating: "Dataframe validating",
      published: "Dataframe published", failed: "Dataframe failed"})[v.bundle],
  "Release": ({previous: "Earlier release live", this: "This load live", newer: "Newer load live"})[v.release],
});
export const step = (a) => ({
  FilesCopied: "all files are copied", Claim: "a worker claims the publish lease",
  LeaseLost: "the worker dies or loses its lease", Stage: "the worker stages the rows",
  BuildDataframe: "the worker builds the dataframe", FinishWriting: "the dataframe write finishes",
  PassValidation: "the dataframe passes validation", BuildFails: "the dataframe build fails",
  GoLive: "release, views and receipt commit together", FindNewerRelease: "the worker sees a newer release",
  OtherLoadGoesLive: "another load goes live",
  Done: null,
})[a] ?? a;
