import { NextApiRequest, NextApiResponse } from "next";
import { getSvix } from "@/auth";

interface DataIn {
  username: string;
  company: string;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse<any>) {
  const data: DataIn = req.body;

  // Do real signup stuff
  // ...

  // Create Svix application (can also do lazily at a later stage. See docs)
  const svix = getSvix(data.username);

  // Create an application with a uid so that we can refer to it later
  // We are using getOrCreate to avoid conflicts in this fake signup flow.
  try {
    await svix.application.getOrCreate({ name: data.company, uid: data.username });
    res.json({});
  } catch (e: any) {
    console.error("Svix Error:", JSON.stringify(e, null, 2));
    if (e.body) {
      console.error("Svix Error Body:", JSON.stringify(e.body, null, 2));
    }
    res.status(500).json({ error: e.message });
  }
}
