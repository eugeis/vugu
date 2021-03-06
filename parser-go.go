package vugu

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cespare/xxhash"

	"golang.org/x/net/html/atom"

	"golang.org/x/net/html"
)

// ParserGo is a template parser that emits Go source code that will construct the appropriately wired VGNodes.
type ParserGo struct {
	PackageName   string // name of package to use at top of files
	ComponentType string // just the struct name, no "*"
	DataType      string // just the struct name, no "*"
	OutDir        string // output dir
	OutFile       string // output file name with ".go" suffix
}

func (p *ParserGo) gofmt(pgm string) (string, error) {

	tmpf, err := ioutil.TempFile(p.OutDir, "ParserGo")
	if err != nil {
		return "", err
	}
	tmpf.Close()
	tmpfName := tmpf.Name() + ".go"
	err = os.Rename(tmpf.Name(), tmpfName)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpfName)

	err = ioutil.WriteFile(tmpfName, []byte(pgm), 0600)
	if err != nil {
		return "", err
	}

	b, err := exec.Command("gofmt", tmpfName).CombinedOutput()
	if err != nil {
		return pgm, fmt.Errorf("go fmt error %v; full output: %s", err, b)
	}

	return string(b), nil
}

// Parse will parse a .vugu file and write out a Go source code file to OutFile.
func (p *ParserGo) Parse(r io.Reader) error {

	nodeList, err := html.ParseFragment(r, cruftBody)
	if err != nil {
		return err
	}

	var nroot, nscript, nstyle *html.Node

	// TODO: move this top chunk into parser.go

	// look for a script type="application/x-go", a style tag and one other element
nodeLoop1:
	for _, n := range nodeList {
		if n.Type == html.ElementNode {

			if n.DataAtom == atom.Style {
				if nstyle != nil {
					return fmt.Errorf("more than one <style> tag not allowed")
				}
				nstyle = n
				continue nodeLoop1
			}

			if n.DataAtom == atom.Script {

				for _, a := range n.Attr {
					if a.Key == "type" && a.Val == "application/x-go" {

						if nscript != nil {
							return fmt.Errorf("more than one <script type=%q> tag not allowed", a.Val)
						}

						nscript = n

						continue nodeLoop1

					}
				}

				return fmt.Errorf("<script> tag without type=%q not allowed", "application/x-go")
			}

			if nroot != nil {
				return fmt.Errorf("found more than one root elements, not allowed")
			}
			nroot = n

		}
	}
	if nroot == nil {
		return fmt.Errorf("no root element")
	}

	if nroot.Parent != nil {
		panic(fmt.Errorf("nroot should not have a parent"))
	}

	var buf bytes.Buffer

	fmt.Fprintf(&buf, "package %s\n\n", p.PackageName)
	fmt.Fprintf(&buf, "// DO NOT EDIT: This file was generated by vugu. Please regenerate instead of editing or add additional code in a separate file.\n\n")
	fmt.Fprintf(&buf, "import %q\n", "fmt")
	fmt.Fprintf(&buf, "import %q\n", "reflect")
	fmt.Fprintf(&buf, "import %q\n", "github.com/vugu/vugu")
	fmt.Fprintf(&buf, "\n")

	// implement nscript -  dump the code here
	// TODO: do we need some basic smarts? like moving imports to the top, etc.
	if nscript != nil {
		txtNode := nscript.FirstChild
		if txtNode.Type != html.TextNode {
			return fmt.Errorf("found script tag with bad contents (wrong type): %#v", txtNode)
		}
		// dump the source right in there
		fmt.Fprintf(&buf, "%s\n", txtNode.Data)
	}

	// statically check that we implement vugu.ComponentType
	fmt.Fprintf(&buf, "var _ vugu.ComponentType = (*%s)(nil)\n", p.ComponentType)
	fmt.Fprintf(&buf, "\n")

	// FIXME: we should only output the struct here if it's not in a <script type="application/x-go"> tag
	// fmt.Fprintf(&buf, "type %s struct {\n", p.ComponentType)
	// fmt.Fprintf(&buf, "}\n")
	// fmt.Fprintf(&buf, "\n")

	// fmt.Fprintf(&buf, "func (c %s) TagName() string { return %q }\n", p.ComponentType, p.TagName)
	// fmt.Fprintf(&buf, "\n")

	fmt.Fprintf(&buf, "func (comp *%s) BuildVDOM(dataI interface{}) (vdom *vugu.VGNode, css *vugu.VGNode, reterr error) {\n", p.ComponentType)
	fmt.Fprintf(&buf, "    data := dataI.(*%s)\n", p.DataType)
	fmt.Fprintf(&buf, "    _ = data\n")
	fmt.Fprintf(&buf, "    _ = fmt.Sprint\n")
	fmt.Fprintf(&buf, "    _ = reflect.Value{}\n")
	fmt.Fprintf(&buf, "    event := vugu.DOMEventStub\n")
	fmt.Fprintf(&buf, "    _ = event\n")

	// implement nstyle, if present, make it assign to css var
	if nstyle != nil && nstyle.FirstChild != nil {
		fmt.Fprintf(&buf, "css = &vugu.VGNode{Type:vugu.VGNodeType(%d),Data:%q,DataAtom:vugu.VGAtom(%d),Namespace:%q,Attr:%#v}\n",
			nstyle.Type, nstyle.Data, nstyle.DataAtom, nstyle.Namespace, staticVGAttr(nstyle.Attr))
		fmt.Fprintf(&buf, "css.AppendChild(&vugu.VGNode{Type:vugu.VGNodeType(%d),Data:%q,DataAtom:vugu.VGAtom(%d),Namespace:%q,Attr:%#v})\n",
			nstyle.FirstChild.Type, nstyle.FirstChild.Data, nstyle.FirstChild.DataAtom, nstyle.FirstChild.Namespace, staticVGAttr(nstyle.FirstChild.Attr))
	}

	fmt.Fprintf(&buf, "    var n *vugu.VGNode\n")

	// TODO: while gofmt will fix indentation, it makes debugging bad source code that won't gofmt much harder - we should really do
	// at least some basic indentation ourselves so the pre-gofmt output is more human readable

	// depth := 0
	n := nroot
	// count := 0
	closeReq := make(map[*html.Node]bool)

writeNode:
	for n != nil {

		// count++
		// if count > 1000 {
		// 	panic(fmt.Errorf("too many"))
		// }

		// var outAttr []VGAttribute
		// for _, a := range n.Attr {
		// 	switch {
		// 	case a.Key == "vg-if":
		// 		// vgn.VGIf = attrFromHtml(a)
		// 	case a.Key == "vg-for":
		// 		// vgn.VGRange = attrFromHtml(a)
		// 	case strings.HasPrefix(a.Key, ":"):
		// 		// vgn.BindAttr = append(vgn.BindAttr, attrFromHtml(a))
		// 	case strings.HasPrefix(a.Key, "@"):
		// 		// vgn.EventAttr = append(vgn.EventAttr, attrFromHtml(a))
		// 	default:
		// 		outAttr = append(outAttr, attrFromHtml(a))
		// 	}
		// }

		// Type      VGNodeType
		// DataAtom  VGAtom
		// Data      string
		// Namespace string
		// Attr      []VGAttribute

		// output additional block cases and record in closeReq - these are mutually exclusive, at least for now
		if ifExpr := vgIfExpr(n); ifExpr != "" {
			fmt.Fprintf(&buf, "if %s {\n", ifExpr)
			closeReq[n] = true
		} else if forExpr := vgForExpr(n); forExpr != "" {
			fmt.Fprintf(&buf, "for %s {\n", forExpr)
			if strings.HasPrefix(forExpr, "key, value :=") {
				fmt.Fprintf(&buf, "_, _ = key, value\n") // people using the shorthand often won't use the key, this should not cause a compile error
			}
			closeReq[n] = true
		}

		// TODO: we should output in a Go comment above this the HTML, for ease of debugging
		// TODO: even better would be if it had the line number from the original input (possibly we can keep track of this by just counting the \ns we encounter)
		fmt.Fprintf(&buf, "n = &vugu.VGNode{Type:vugu.VGNodeType(%d),Data:%q,DataAtom:vugu.VGAtom(%d),Namespace:%q,Attr:%#v}\n", n.Type, n.Data, n.DataAtom, n.Namespace, staticVGAttr(n.Attr))
		if n != nroot {
			fmt.Fprintf(&buf, "parent.AppendChild(n)\n") // if not root, make AppendChild call
		} else {
			fmt.Fprintf(&buf, "vdom = n\n") // special case for root
		}

		if htmlExpr := vgHTMLExpr(n); htmlExpr != "" {
			fmt.Fprintf(&buf, "n.InnerHTML = fmt.Sprint(%s)\n", htmlExpr)
		}

		// dynamic attributes with ":" prefix
		propm := vgPropExprs(n)
		if len(propm) > 0 {
			fmt.Fprintf(&buf, "n.Props = vugu.Props {\n")
			keys := make([]string, 0, len(propm))
			for k := range propm {
				keys = append(keys, k)
			}
			sort.Strings(keys) // stable output sequence
			for _, k := range keys {
				fmt.Fprintf(&buf, "%q: %s,\n", k, propm[k])
			}
			fmt.Fprintf(&buf, "}\n")
		}

		// DOM events
		dome := vgDOMEventExprs(n)
		if len(dome) > 0 {

			// fmt.Fprintf(&buf, "n.Props = vugu.Props {\n")
			keys := make([]string, 0, len(dome))
			for k := range dome {
				keys = append(keys, k)
			}
			sort.Strings(keys) // stable output sequence
			for _, k := range keys {

				expr := dome[k]
				receiver, methodName, argList := vgDOMParseExpr(expr)
				if methodName == "" {
					return fmt.Errorf("unable to parse DOM event handler expression %q", expr)
				}

				fmt.Fprintf(&buf, "// @%s = { %s }\n", k, expr)
				if receiver != "" {
					fmt.Fprintf(&buf, "{\n")
					fmt.Fprintf(&buf, "var i_ interface{} = %s\n", receiver)
					fmt.Fprintf(&buf, "idat_ := reflect.ValueOf(&i_).Elem().InterfaceData()\n")
					fmt.Fprintf(&buf, "var i2_ interface{} = %s.%s\n", receiver, methodName)
					fmt.Fprintf(&buf, "i2dat_ := reflect.ValueOf(&i2_).Elem().InterfaceData()\n")
					fmt.Fprintf(&buf, "n.SetDOMEventHandler(%q, vugu.DOMEventHandler{\n", k)
					fmt.Fprintf(&buf, "    ReceiverAndMethodHash: uint64(idat_[0]) ^ uint64(idat_[1]) ^ uint64(i2dat_[0]) ^ uint64(i2dat_[1]),\n")
					fmt.Fprintf(&buf, "    Method: reflect.ValueOf(%s).MethodByName(%q),\n", receiver, methodName)
					fmt.Fprintf(&buf, "    Args: []interface{}{%s},\n", argList)
					fmt.Fprintf(&buf, "})\n")
					fmt.Fprintf(&buf, "}\n")
				} else {
					fmt.Fprintf(&buf, "n.SetDOMEventHandler(%q, vugu.DOMEventHandler{\n", k)
					fmt.Fprintf(&buf, "    ReceiverAndMethodHash: %d,\n", xxhash.Sum64String(methodName)) // it's just the method name, so a simple static hash will do
					fmt.Fprintf(&buf, "    Method: reflect.ValueOf(%s),\n", methodName)
					fmt.Fprintf(&buf, "    Args: []interface{}{%s},\n", argList)
					fmt.Fprintf(&buf, "})\n")
				}
				fmt.Fprintf(&buf, "if false {\n")
				fmt.Fprintf(&buf, "// force compiler to check arguments for type safety\n")
				fmt.Fprintf(&buf, "%s.%s(%s)\n", receiver, methodName, argList)
				fmt.Fprintf(&buf, "}\n")

				// // output type check
				// fmt.Fprintf(&buf, "if false {\n")
				// fmt.Fprintf(&buf, "    %s // force compiler to check syntax and types, even though actual call is done through reflection\n", expr)
				// fmt.Fprintf(&buf, "}\n")
			}
		}

		// vgn.SetDOMEventHandler("click", vugu.DOMEventHandler{
		// 		Method: reflect.ValueOf(comp).MethodByName("HandleClick"),
		// 		Args: []interface{}{event, rowVal},
		// })
		// if false {
		// 	comp.HandleClick(event, rowVal) // force compiler to check types, even though actual call is done through reflection
		// }

		// log.Printf("n = %#v", n)

		// is there a child to descend into
		if n.FirstChild != nil {
			// log.Printf("going down")
			// depth++
			n = n.FirstChild

			// if ifExpr := vgIfExpr(n); ifExpr != "" {
			// 	fmt.Fprintf(&buf, "%s {\n", ifExpr)
			// } else if forExpr := vgForExpr(n); forExpr != "" {
			// 	fmt.Fprintf(&buf, "%s {\n", forExpr)
			// } else {
			// 	fmt.Fprintf(&buf, "{\n")
			// }
			fmt.Fprintf(&buf, "{\n")
			fmt.Fprintf(&buf, "parent := n\n")
			continue writeNode
		}

		// is there a next sibling to move onto
		if n.NextSibling != nil {
			// log.Printf("going next")

			if closeReq[n] { // close if/for block as needed
				fmt.Fprintf(&buf, "}\n")
			}

			n = n.NextSibling
			continue writeNode
		}

		// only thing we can do is go back up toward root
		// log.Printf("going up")

		if closeReq[n] { // close if/for block as needed
			fmt.Fprintf(&buf, "}\n")
		}

		for n = n.Parent; n != nil; n = n.Parent {

			if closeReq[n] { // close if/for block as needed
				fmt.Fprintf(&buf, "}\n")
			}

			fmt.Fprintf(&buf, "}\n")
			if n.NextSibling != nil {
				n = n.NextSibling
				continue writeNode
			}
		}
		// if n.Parent != nil {
		// 	// depth--
		// 	var nnew *html.Node
		// 	for nnew == nil {
		// 		nnew = n.Parent.NextSibling
		// 		n = n.Parent
		// 		if n == nil {
		// 			break writeNode
		// 		}
		// 	}
		// 	fmt.Fprintf(&buf, "}\n")
		// 	continue writeNode
		// }

		// no place to go, we're done
		// log.Printf("done")
		// n = nil
		continue writeNode
	}

	// TODO: figure out how to go from *html.Node to Go source that produces a VGNode, recursively
	fmt.Fprintf(&buf, "    return\n")
	fmt.Fprintf(&buf, "}\n")
	fmt.Fprintf(&buf, "\n")

	s, err := p.gofmt(buf.String())
	if err != nil {
		// if gofmt fails, attempt to write no-gofmt'ed program to the file, ignore the error - useful for debugging
		ioutil.WriteFile(filepath.Join(p.OutDir, p.OutFile), []byte(s), 0644)
		return err
	}

	return ioutil.WriteFile(filepath.Join(p.OutDir, p.OutFile), []byte(s), 0644)

}
