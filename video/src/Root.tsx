import { Composition, Folder } from "remotion";
import { Launch } from "./Launch";

// 37 s at 30 fps, like the original launch film (docs/assets/tenon-launch.mp4).
export const RemotionRoot: React.FC = () => {
  return (
    <>
      <Folder name="LinkedIn">
        <Composition id="Launch-Square-EN" component={Launch} durationInFrames={1110} fps={30} width={1080} height={1080} defaultProps={{ lang: "en" as const }} />
        <Composition id="Launch-Square-FR" component={Launch} durationInFrames={1110} fps={30} width={1080} height={1080} defaultProps={{ lang: "fr" as const }} />
      </Folder>
      <Folder name="Reels-Shorts">
        <Composition id="Launch-Vertical-EN" component={Launch} durationInFrames={1110} fps={30} width={1080} height={1920} defaultProps={{ lang: "en" as const }} />
        <Composition id="Launch-Vertical-FR" component={Launch} durationInFrames={1110} fps={30} width={1080} height={1920} defaultProps={{ lang: "fr" as const }} />
      </Folder>
      <Folder name="Site">
        <Composition id="Launch-Landscape-EN" component={Launch} durationInFrames={1110} fps={30} width={1920} height={1080} defaultProps={{ lang: "en" as const }} />
        <Composition id="Launch-Landscape-FR" component={Launch} durationInFrames={1110} fps={30} width={1920} height={1080} defaultProps={{ lang: "fr" as const }} />
      </Folder>
    </>
  );
};
