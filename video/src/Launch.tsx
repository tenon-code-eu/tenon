// The tenon launch film, after the storyboard .tools/launch-video/index.html:
// A shield and stars · B approval · C where your data goes · D audit chain · E install.
import React, { useEffect, useState } from "react";
import { AbsoluteFill, Series, continueRender, delayRender, useVideoConfig } from "remotion";
import { fontsReady } from "./fonts";
import type { Lang } from "./i18n";
import { Control } from "./scenes/Control";
import { Flow } from "./scenes/Flow";
import { Proof } from "./scenes/Proof";
import { ShieldScene } from "./scenes/ShieldScene";
import { Backdrop } from "./ui";

export type LaunchProps = { lang: Lang };

export const Launch: React.FC<LaunchProps> = ({ lang }) => {
  const { fps } = useVideoConfig();
  const [handle] = useState(() => delayRender("tenon fonts"));
  const [ready, setReady] = useState(false);
  useEffect(() => {
    fontsReady.then(() => { setReady(true); continueRender(handle); });
  }, [handle]);
  return (
    <AbsoluteFill>
      <Backdrop />
      {ready && (
        <Series>
          <Series.Sequence name="A · shield" durationInFrames={7 * fps}>
            <ShieldScene lang={lang} mode="intro" durationInFrames={7 * fps} />
          </Series.Sequence>
          <Series.Sequence name="B · approval" durationInFrames={7.5 * fps}>
            <Control lang={lang} durationInFrames={7.5 * fps} />
          </Series.Sequence>
          <Series.Sequence name="C · data" durationInFrames={8 * fps}>
            <Flow lang={lang} durationInFrames={8 * fps} />
          </Series.Sequence>
          <Series.Sequence name="D · proof" durationInFrames={7 * fps}>
            <Proof lang={lang} durationInFrames={7 * fps} />
          </Series.Sequence>
          <Series.Sequence name="E · install" durationInFrames={7.5 * fps}>
            <ShieldScene lang={lang} mode="outro" durationInFrames={7.5 * fps} />
          </Series.Sequence>
        </Series>
      )}
    </AbsoluteFill>
  );
};
