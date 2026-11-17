// SPDX-License-Identifier: Apache-2.0
//
// Where a check's firings go is decided the same way for every check:
// its own channels when it names some, else the notification profile
// that applies (the integration's, the team's default, the org default).
//
// The lists showed only a check's own channels, so a profile-routed
// check looked as if it went nowhere, the integration's health checks
// had no way to name channels, and a trace-completion rule with nothing
// ticked sent its first page to no one. The lists now say where each
// check sends, and every check form has the same "Send to" field.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test("checks send through the integration's profile unless they name their own channels", async ({ page }) => {
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  const cleanup: { rules: string[]; channels: string[]; profiles: string[]; integration?: string } = {
    rules: [],
    channels: [],
    profiles: [],
  };
  const channel = async (name: string) => {
    const res = await admin.post("/api/v1/notification-channels", {
      data: { name, kind: "webhook", config: { url: "http://127.0.0.1:9/e2e-send-to" } },
    });
    expect(res.ok(), `create channel: ${res.status()}`).toBeTruthy();
    const id = (await res.json()).id as string;
    cleanup.channels.push(id);
    return id;
  };

  try {
    const mk = await admin.post("/api/v1/integrations", {
      data: {
        slug: `e2e-send-to-${stamp}`,
        name: `E2E Send To ${stamp}`,
        matchers: [{ operator: "equals", value: `e2e-send-to-svc-${stamp}` }],
      },
    });
    expect(mk.ok(), `create integration: ${mk.status()}`).toBeTruthy();
    const integ = (await mk.json()).integration.id as string;
    cleanup.integration = integ;

    const viaProfile = await channel(`e2e-sendto-profile-${stamp}`);
    const own = await channel(`e2e-sendto-own-${stamp}`);
    const prof = await admin.post("/api/v1/notification-profiles", { data: { name: `e2e-sendto-${stamp}` } });
    expect(prof.ok(), `create profile: ${prof.status()}`).toBeTruthy();
    const profID = (await prof.json()).id as string;
    cleanup.profiles.push(profID);
    expect((await admin.put(`/api/v1/notification-profiles/${profID}/channels`, { data: { channel_ids: [viaProfile] } })).ok()).toBeTruthy();
    expect((await admin.put(`/api/v1/integrations/${integ}/notification-profile`, { data: { profile_id: profID } })).ok()).toBeTruthy();

    const check = async (name: string, channelIDs: string[]) => {
      const res = await admin.post("/api/v1/alert-rules", {
        data: {
          name,
          severity: "warning",
          enabled: false,
          signal: "trace",
          integration_id: integ,
          channel_ids: channelIDs,
          trace_volume_spec: { kind: "trace_volume", threshold: 1, window_seconds: 300 },
        },
      });
      expect(res.ok(), `create check: ${res.status()} ${await res.text()}`).toBeTruthy();
      const id = (await res.json()).id as string;
      cleanup.rules.push(id);
      return id;
    };
    const byProfile = await check(`e2e-sendto-profile-${stamp}`, []);
    const byOwn = await check(`e2e-sendto-own-${stamp}`, [own]);

    const list = await (await admin.get(`/api/v1/alert-rules?integration=${integ}`)).json();
    expect(list.delivery[byProfile]).toMatchObject({ via: "profile", profile_id: profID, channel_ids: [viaProfile] });
    expect(list.delivery[byOwn]).toMatchObject({ via: "channels", channel_ids: [own] });

    const completion = await admin.post(`/api/v1/integrations/${integ}/completion-rules`, {
      data: {
        name: `e2e-sendto-completion-${stamp}`,
        severity: "warning",
        enabled: false,
        start_span_name: "start",
        stages: [{ spans: [{ span_name: "done" }], require: "any" }],
        default_timeout_seconds: 300,
        channel_ids: [],
      },
    });
    expect(completion.ok(), `create completion rule: ${completion.status()} ${await completion.text()}`).toBeTruthy();
    const crules = await (await admin.get(`/api/v1/integrations/${integ}/completion-rules`)).json();
    expect(crules.rules[0].delivery, "a completion rule without channels follows the profile too").toMatchObject({
      via: "profile",
      profile_id: profID,
    });

    // The integration's lists say it in words.
    await page.goto(`/integrations/${integ}/settings?tab=alerting`);
    await expect(page.getByText(`Profile “e2e-sendto-${stamp}”: e2e-sendto-profile-${stamp}`).first()).toBeVisible();
    await expect(page.getByText(`e2e-sendto-own-${stamp} (own)`)).toBeVisible();

    // And the health check form offers the same choice as every other.
    await page.getByRole("button", { name: "+ Add health check" }).click();
    await page.getByRole("menuitem").first().click();
    const sendTo = page.getByTestId("send-to").getByRole("combobox", { name: "Send to" });
    await expect(sendTo).toHaveValue("profile");
    await sendTo.selectOption("channels");
    await expect(page.getByTestId("send-to").getByText(`e2e-sendto-own-${stamp}`)).toBeVisible();
  } finally {
    for (const id of cleanup.rules) await admin.delete(`/api/v1/alert-rules/${id}`);
    if (cleanup.integration) await admin.delete(`/api/v1/integrations/${cleanup.integration}`);
    for (const id of cleanup.profiles) await admin.delete(`/api/v1/notification-profiles/${id}`);
    for (const id of cleanup.channels) await admin.delete(`/api/v1/notification-channels/${id}`);
  }
});
