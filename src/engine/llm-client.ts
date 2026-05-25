import Anthropic from "@anthropic-ai/sdk";

export interface ILlmClient {
  generate(prompt: string, systemPrompt: string): Promise<string>;
  readonly modelId: string;
}

export class AnthropicLlmClient implements ILlmClient {
  private client: Anthropic;
  readonly modelId: string;

  constructor(modelId: string, apiKey?: string) {
    this.modelId = modelId;
    this.client = new Anthropic({ apiKey });
  }

  async generate(prompt: string, systemPrompt: string): Promise<string> {
    const response = await this.client.messages.create({
      model: this.modelId,
      max_tokens: 16384,
      system: systemPrompt,
      messages: [{ role: "user", content: prompt }],
    });

    const textBlocks = response.content.filter((b) => b.type === "text");
    return textBlocks.map((b) => b.text).join("\n");
  }
}
