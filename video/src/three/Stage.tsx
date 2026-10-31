// A three.js scene rendered by Remotion: the scene graph is built once, mutated from the
// current frame, and the camera is computed outside so HTML labels can be projected on it.
import { ThreeCanvas } from "@remotion/three";
import { useThree } from "@react-three/fiber";
import React from "react";
import * as THREE from "three";
import { AbsoluteFill, useVideoConfig } from "remotion";

const CamSync: React.FC<{ cam: THREE.PerspectiveCamera; tick: string }> = ({ cam }) => {
  const camera = useThree((s) => s.camera) as THREE.PerspectiveCamera;
  camera.position.copy(cam.position);
  camera.quaternion.copy(cam.quaternion);
  camera.fov = cam.fov;
  camera.near = cam.near;
  camera.far = cam.far;
  camera.updateProjectionMatrix();
  return null;
};

export const Stage: React.FC<{ root: THREE.Object3D; cam: THREE.PerspectiveCamera; children?: React.ReactNode }> = ({ root, cam, children }) => {
  const { width, height } = useVideoConfig();
  const tick = cam.position.toArray().join() + cam.quaternion.toArray().join() + cam.fov;
  return (
    <AbsoluteFill>
      <ThreeCanvas width={width} height={height} flat gl={{ antialias: true, alpha: true }} camera={{ fov: cam.fov, near: cam.near, far: cam.far }}>
        <CamSync cam={cam} tick={tick} />
        <primitive object={root} />
      </ThreeCanvas>
      {children}
    </AbsoluteFill>
  );
};

/* Camera looking at target from eye, with the composition's aspect ratio. */
export function makeCamera(fov: number, width: number, height: number, eye: THREE.Vector3, target: THREE.Vector3) {
  const cam = new THREE.PerspectiveCamera(fov, width / height, 0.1, 400);
  cam.position.copy(eye);
  cam.lookAt(target);
  cam.updateMatrixWorld();
  cam.updateProjectionMatrix();
  return cam;
}

/* Screen position (px) of a world point. */
export function project(p: THREE.Vector3, cam: THREE.PerspectiveCamera, width: number, height: number) {
  const v = p.clone().project(cam);
  return { x: (v.x * 0.5 + 0.5) * width, y: (1 - (v.y * 0.5 + 0.5)) * height, behind: v.z > 1 };
}

/* World units per pixel at distance d (to place 3D objects by screen layout). */
export const unitsPerPx = (fov: number, d: number, height: number) => (2 * d * Math.tan((fov * Math.PI) / 360)) / height;
