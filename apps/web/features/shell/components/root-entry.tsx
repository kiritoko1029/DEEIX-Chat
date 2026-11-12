"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

export function RootEntry() {
  const router = useRouter();

  React.useEffect(() => {
    router.replace("/chat");
  }, [router]);

  return null;
}
