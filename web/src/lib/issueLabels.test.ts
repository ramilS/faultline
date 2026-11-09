import assert from "node:assert/strict";
import { test } from "node:test";

import { issueCulprit, issueTitle } from "./issueLabels.ts";

test("an issue with display labels shows them over the raw ones", () => {
  const issue = {
    title: "bu4: Source error",
    culprit: "Muslicast+0xfa6408",
    display_title: "SourceException: Source error",
    display_culprit: "PlaybackController.invoke (PlaybackController.swift)"
  };

  assert.equal(issueTitle(issue), "SourceException: Source error");
  assert.equal(issueCulprit(issue), "PlaybackController.invoke (PlaybackController.swift)");
});

test("an issue without display labels shows the raw ones", () => {
  const issue = { title: "TypeError: x is undefined", culprit: "app.js.render", display_title: "", display_culprit: "" };

  assert.equal(issueTitle(issue), "TypeError: x is undefined");
  assert.equal(issueCulprit(issue), "app.js.render");
});
