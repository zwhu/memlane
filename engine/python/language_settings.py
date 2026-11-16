"""Application default language. Switching is reserved for a later release."""

DEFAULT_LANGUAGE = 'zh-CN'


def language_settings():
    return {'language': DEFAULT_LANGUAGE, 'display_name': '简体中文', 'configurable': False}


def knowledge_language_instructions():
    return '''

MEMLANE DEFAULT LANGUAGE (zh-CN):
Write all natural-language candidate lessons, working_statement, statement,
why_learned and explanatory summaries in Simplified Chinese (简体中文), even when
the conversation, tool output, existing lessons or instructions above are English.
Keep JSON field names, enum values, state keys, entity identifiers, file paths,
commands, function names and code exactly as required by the original schema.
Express the explanation in concise Chinese; do not translate technical identifiers.
An existing English lesson stating the same fact is still already known: a language
change alone is not new knowledge and must not bypass the original durability,
evidence, deduplication or acceptance rules. Do not add a translation-only lesson.
'''
