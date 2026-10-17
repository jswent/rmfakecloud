import { useState, useEffect, useRef, useMemo } from "react";
import { Button, ButtonGroup, Dropdown } from "react-bootstrap";
import Navbar from 'react-bootstrap/Navbar';
import { FaChevronRight, FaChevronLeft, } from "react-icons/fa6";
import { AiOutlineDownload } from "react-icons/ai";
import constants from "../../common/constants";

import apiservice from "../../services/api.service"
import NameTag from "../../components/NameTag"

import { pdfjs, Document, Page } from "react-pdf";
import styles from "./Documents.module.scss";

export default function FileViewer({ file, onSelect }) {
  const { data } = file;

  const downloadUrl = `${constants.ROOT_URL}/documents/${file.id}`;

  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  // Width of the scrollable viewer. Pages are rendered to fill it
  // horizontally; anything taller than the viewer scrolls vertically, so a
  // tall notebook page (reMarkable pages grow as you write down) stays legible
  // instead of being shrunk to the viewer height.
  const [width, setWidth] = useState(0);
  const onLoadSuccess = (pdf) => {
    setPage(1);
    setPages(pdf.numPages);
  };
  const onPrev = () => {
    setPage((p) => {
      return Math.max(p - 1, 1);
    });
  };
  const onNext = () => {
    setPage((p) => {
      return Math.min(p + 1, pages);
    });
  };
  const viewer = useRef(null);
  useEffect(() => {
    const el = viewer.current;
    if (!el) return;
    const resizeObserver = new ResizeObserver((entries) => {
      // https://developer.mozilla.org/en-US/docs/Web/API/ResizeObserverEntry/contentBoxSize
      const box = entries[0].contentBoxSize[0];
      setWidth(Math.floor(box.inlineSize));
    });
    resizeObserver.observe(el);
    return () => resizeObserver.disconnect();
  }, []);

  // Start every page at its top.
  useEffect(() => {
    if (viewer.current) {
      viewer.current.scrollTop = 0;
    }
  }, [page, file.id]);

  // TODO: add loading and error handling
  const triggerDownload = (blob, filename) => {
    var url = window.URL.createObjectURL(blob)
    var a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  const onDownloadPdf = () => {
    apiservice.download(data.id)
      .then(blob => triggerDownload(blob, data.name + '.pdf'))
      .catch(() => {})
  }

  const onDownloadRmdoc = () => {
    apiservice.download(data.id, 'rmdoc')
      .then(blob => triggerDownload(blob, data.name + '.rmdoc'))
      .catch(() => {})
  }

  let options = useMemo(()=> {
    return {
      worker: new pdfjs.PDFWorker()
    }
  },[pdfjs])

  return (
    <div className={styles.fileViewer}>
      <Navbar style={{ marginLeft: '-12px', flexShrink: 0 }}>
        { file && (<div><NameTag node={file} onSelect={onSelect} /></div>) }
      </Navbar>

      <Navbar style={{ flexShrink: 0 }}>
        {pages > 1 && (
          <div>
            <ButtonGroup aria-label="Page navigation">
              <Button size="sm" variant="outline-secondary" onClick={onPrev} disabled={page <= 1}><FaChevronLeft /></Button>
              <Button size="sm" variant="outline-secondary" onClick={onNext} disabled={page >= pages}><FaChevronRight /></Button>
            </ButtonGroup>
            <span style={{ margin: '0 10px' }}>
              Page: {page} of {pages}
            </span>
          </div>
        )}
        <div style={{ flex: 1 }}></div>

        <Dropdown align="end">
          <Dropdown.Toggle size="sm" variant="secondary">
            <AiOutlineDownload />
          </Dropdown.Toggle>
          <Dropdown.Menu>
            <Dropdown.Item onClick={onDownloadPdf}>Download PDF</Dropdown.Item>
            <Dropdown.Item onClick={onDownloadRmdoc}>Download .rmdoc</Dropdown.Item>
          </Dropdown.Menu>
        </Dropdown>

      </Navbar>

      <div ref={viewer} className={styles.pdfViewer}>
        {file && width > 0 && (
          <Document file={downloadUrl} onLoadSuccess={onLoadSuccess} options={options}>
            <Page pageNumber={page}
              width={width}
              renderAnnotationLayer={false}
              renderTextLayer={false}
            />
          </Document>
        )}
      </div>
    </div>
  );
}
