CRITICAL: You MUST call tools FIRST before responding!

STEP 1 - CALL THIS TOOL NOW:
Tool: agntcy_oasf_get_schema_skills
Args: {"version": "1.1.0"}

Wait for response. The response will show top-level skills like:
{"name": "language_processing", ...}, {"name": "software_engineering", ...}, {"name": "ai_ml_engineering", ...}

STEP 2 - Pick ONE skill "name" from Step 1 (e.g. "software_engineering")

STEP 3 - CALL THIS TOOL NOW:
Tool: agntcy_oasf_get_schema_skills
Args: {"version": "1.1.0", "parent_skill": "YOUR_CHOICE_FROM_STEP_2"}

Wait for response. The response will show sub-skills with "name" and "id" fields like:
{"name": "software_engineering/code_generation", "caption": "Code Generation", "id": 601}
{"name": "software_engineering/web_development", "caption": "Web Development", "id": 602}

STEP 4 - Pick 1-5 sub-skills and extract BOTH "name" and "id" from Step 3

DO NOT INVENT NAMES! These DO NOT exist:
❌ "information_retrieval_synthesis"
❌ "api_server_operations"
❌ "magic_code_writing"
❌ "universal_problem_solving"

Real examples (from actual schema):
✓ "software_engineering/code_generation" with id 601
✓ "software_engineering/web_development" with id 602
✓ "language_processing/language_understanding" with its corresponding id 101
✓ "language_processing/language_generation" with its corresponding id 103

STEP 5 - OUTPUT FORMAT (CRITICAL):
Return ONLY the raw JSON object below. DO NOT wrap in markdown code blocks.
DO NOT use markdown formatting. DO NOT add language tags like "json".
DO NOT add ANY text or explanation before or after the JSON.

Your response must start with "{" and end with "}".

Return exactly this structure:
{
  "skills": [
    {
      "name": "parent_skill/sub_skill",
      "id": 601,
      "confidence": 0.95,
      "reasoning": "Brief explanation"
    }
  ]
}

IMPORTANT: The "id" field MUST be the exact ID returned by the get_schema_skills tool in Step 3.
Do NOT invent or guess IDs. Use only the IDs from the tool response.

Agent record to analyze:

