"""
回归测试：index.html.template 顶部的说明注释不能原样写出模板标记的字面文本。

背景：模板曾经在说明注释里写 `用真实的 Issue 表格行替换 <!-- CODING_FLOW_ISSUES --> 这个标记`。
这有两个问题：
  1. render.py 对模板做 str.replace("<!-- CODING_FLOW_ISSUES -->", rows) 时，
     会连这段说明文字里的字面出现一起替换掉，把注释改得不知所云。
  2. 更严重的是，说明文字里的 `<!-- CODING_FLOW_ISSUES -->` 本身含有 "-->"，
     会让外层 <!-- ... --> 说明注释在真正结束之前就被提前闭合——按 HTML 语法，
     注释在第一个 "-->" 处就结束了，后面本该在注释里的文字会变成页面上可见的内容。

纯标准库 unittest，不解析真实 HTML（没有引入额外依赖），只做字符串层面的不变量检查，
这些检查在修复前会失败、修复后会通过。
"""

import unittest
from pathlib import Path

TEMPLATE_PATH = (
    Path(__file__).resolve().parent.parent / "templates" / "dashboard" / "index.html.template"
)


class TemplateMarkerTest(unittest.TestCase):
    def setUp(self):
        self.text = TEMPLATE_PATH.read_text(encoding="utf-8")

    def test_markers_appear_exactly_once(self):
        """标记字符串只能在真正要被替换的位置出现一次，不能在说明文字里重复出现
        （重复出现就会被 render.py 的 str.replace 一并命中）。"""
        self.assertEqual(self.text.count("<!-- CODING_FLOW_ISSUES -->"), 1)
        self.assertEqual(self.text.count("{{GENERATED_AT}}"), 1)

    def test_top_comment_is_not_prematurely_closed(self):
        """顶部说明注释的第一个 "-->" 必须是这段注释真正的结束位置，
        不能被注释内部意外出现的 "-->" 提前截断。"""
        self.assertTrue(self.text.startswith("<!--"))
        first_close = self.text.index("-->")
        comment_body = self.text[4:first_close]
        # "SKILL.md" 只出现在这段说明注释真正结尾附近的那句话里；
        # 如果注释被提前截断，comment_body 会很短，取不到这个词。
        self.assertIn("SKILL.md", comment_body)


if __name__ == "__main__":
    unittest.main()
